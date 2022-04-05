package main

// go:generate rm -Rf proto_common proto
// go:generate mkdir -p proto_common proto
// go:generate protoc --proto_path=. --go_out=proto_common --go-grpc_out=. --go_opt=paths=source_relative init.proto
// go:generate protoc --proto_path=. --go_out=proto --go-grpc_out=. --go_opt=paths=source_relative qlight-token-manager.proto
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

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
	proto.UnimplementedPluginQLightTokenRefresherServer
	proto_common.UnimplementedPluginInitializerServer
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
	Server string
}

func (c *config) validate() error {
	if c.Server == "" {
		return fmt.Errorf("server must be provided")
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

func (h *QlightTokenManagerPluginImpl) TokenRefresh(ctx context.Context, req *proto.PluginQLightTokenManager_Request) (*proto.PluginQLightTokenManager_Response, error) {
	return &proto.PluginQLightTokenManager_Response{Token: fmt.Sprintf("Hello %s!", req.Server)}, nil
}
