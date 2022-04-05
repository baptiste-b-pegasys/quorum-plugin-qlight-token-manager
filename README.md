## Prerequisites

* Go 1.16.x
* Make

## How-to

* Run `make` to create plugin distribution zip files for different OSes. 
* Copy the zip file in the desired OS folder to Quorum plugin folder.
* Define `qlight-token-manager` block in the `providers` section of plugin settings JSON
   ```
   "qlight-token-manager": {
      "name":"quorum-plugin-qlight-token-manager",
      "version":"1.0.0",
      "config": "file://<path-to>/qlight-token-manager-plugin-config.json"
   }
   ```