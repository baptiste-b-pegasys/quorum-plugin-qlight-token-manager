package main

// go:generate rm -Rf proto_common proto
// go:generate mkdir -p proto_common proto
// go:generate protoc --proto_path=. --go_out=proto_common --go-grpc_out=. --go_opt=paths=source_relative init.proto
// go:generate protoc --proto_path=. --go_out=proto --go-grpc_out=. --go_opt=paths=source_relative qlight-token-manager.proto
import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/baptiste-b-pegasys/quorum-plugin-qlight-token-manager/proto"
	"github.com/baptiste-b-pegasys/quorum-plugin-qlight-token-manager/proto_common"
	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	DefaultProtocolVersion = 1
)

var (
	DefaultHandshakeConfig = plugin.HandshakeConfig{
		ProtocolVersion:  DefaultProtocolVersion,
		MagicCookieKey:   "QUORUM_PLUGIN_MAGIC_COOKIE",
		MagicCookieValue: "CB9F51969613126D93468868990F77A8470EB9177503C5A38D437FEFF7786E0941152E05C06A9A3313391059132A7F9CED86C0783FE63A8B38F01623C8257664",
	}
)

// this is to demonstrate how to write a plugin that implements QLight token manager plugin interface
func main() {
	log.SetFlags(0) // don't display time
	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: DefaultHandshakeConfig,
		Plugins: map[string]plugin.Plugin{
			"impl": &QlightTokenManagerPluginImpl{},
		},
		GRPCServer: plugin.DefaultGRPCServer,
	})
}

// implements 3 interfaces:
// 1. Initializer plugin interface - mandatory
// 2. QLight token manager plugin interface
// 3. GRPC Plugin from go-plugin
type QlightTokenManagerPluginImpl struct {
	proto_common.UnimplementedPluginInitializerServer
	proto.UnimplementedPluginQLightTokenRefresherServer
	plugin.Plugin
	cfg *config
}

var _ proto_common.PluginInitializerServer = &QlightTokenManagerPluginImpl{}
var _ proto.PluginQLightTokenRefresherServer = &QlightTokenManagerPluginImpl{}

func (h *QlightTokenManagerPluginImpl) GRPCServer(_ *plugin.GRPCBroker, s *grpc.Server) error {
	proto_common.RegisterPluginInitializerServer(s, h)
	proto.RegisterPluginQLightTokenRefresherServer(s, h)
	return nil
}

func (h *QlightTokenManagerPluginImpl) GRPCClient(context.Context, *plugin.GRPCBroker, *grpc.ClientConn) (interface{}, error) {
	return nil, errors.New("not supported")
}

type config struct {
	URL, Method                      string
	RefreshAnticipationInMillisecond int32
	Parameters                       map[string]string
}

func (c *config) validate() error {
	if c.URL == "" {
		return fmt.Errorf("url must be provided")
	}
	if c.Method == "" {
		return fmt.Errorf("method must be provided")
	}
	return nil
}

func (h *QlightTokenManagerPluginImpl) Init(_ context.Context, req *proto_common.PluginInitialization_Request) (*proto_common.PluginInitialization_Response, error) {
	var cfg config
	if err := json.Unmarshal(req.RawConfiguration, &cfg); err != nil {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid config: %s, err: %s", string(req.RawConfiguration), err.Error()))
	}
	if err := cfg.validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	h.cfg = &cfg
	return &proto_common.PluginInitialization_Response{}, nil
}

type JWT struct {
	ExpireAt int64 `json:"exp"`
}

type OryResp struct {
	AccessToken      string `json:"access_token"`
	Error            string `json:"error,omitempty"`
	ErrorDescription string `json:"error_description,omitempty"`
}

func (h *QlightTokenManagerPluginImpl) PluginQLightTokenManager(ctx context.Context, req *proto.PluginQLightTokenManager_Request) (*proto.PluginQLightTokenManager_Response, error) {
	return &proto.PluginQLightTokenManager_Response{RefreshAnticipationInMillisecond: h.cfg.RefreshAnticipationInMillisecond}, nil
}

func (h *QlightTokenManagerPluginImpl) TokenRefresh(ctx context.Context, req *proto.TokenRefresh_Request) (*proto.TokenRefresh_Response, error) {
	log.Printf("refresh token %s\n", req.GetCurrentToken())
	token := req.GetCurrentToken()
	idx := strings.Index(token, " ")
	if idx >= 0 {
		token = token[idx+1:]
	}
	log.Printf("token=%s\n", token)
	split := strings.Split(token, ".")
	log.Printf("split=%v\n", split)
	data, err := base64.RawStdEncoding.DecodeString(split[1])
	if err != nil {
		return nil, err
	}
	log.Printf("json=%s\n", string(data))
	jwt := &JWT{}
	err = json.Unmarshal(data, jwt)
	if err != nil {
		return nil, err
	}
	log.Printf("expireAt=%v\n", jwt.ExpireAt)
	expireAt := time.Unix(jwt.ExpireAt, 0)
	log.Printf("expireAt=%v\n", expireAt)
	if time.Since(expireAt) < -time.Minute {
		log.Println("return current token")
		return &proto.TokenRefresh_Response{Token: req.GetCurrentToken()}, nil
	}
	transCfg := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // ignore expired SSL certificates
	}
	client := &http.Client{Transport: transCfg}
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	for key, template := range h.cfg.Parameters {
		fw, err := writer.CreateFormField(key)
		if err != nil {
			return nil, err
		}
		_, err = io.Copy(fw, strings.NewReader(strings.Replace(template, "${PSI}", req.Psi, -1)))
		if err != nil {
			return nil, err
		}
	}
	err = writer.Close()
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, h.cfg.Method, h.cfg.URL, bytes.NewReader(body.Bytes()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err = ioutil.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	log.Printf("ory response=%s\n", string(data))
	oryResp := &OryResp{}
	err = json.Unmarshal(data, oryResp)
	if err != nil {
		return nil, err
	}
	if len(oryResp.Error) > 0 {
		return nil, fmt.Errorf("%s: %s", oryResp.Error, oryResp.ErrorDescription)
	}
	token = "bearer " + oryResp.AccessToken
	return &proto.TokenRefresh_Response{Token: token}, nil
}

// accessToken=$$(curl -k -s -X POST -F "grant_type=client_credentials" -F "client_id=$${PSI}" -F "client_secret=foofoo" -F "scope=rpc://eth_* p2p://qlight rpc://admin_* rpc://personal_* rpc://quorumExtension_* rpc://rpc_modules psi://$${PSI}?self.eoa=0x0&node.eoa=0x0" -F "audience=Node1" https://multi-tenancy-oauth2-server:4444/oauth2/token | jq '.access_token' -r)
