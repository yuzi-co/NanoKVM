package auth

import (
	"errors"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/apikey"
)

func (s *Service) CreateAPIKey(c *gin.Context) {
	var req proto.CreateAPIKeyReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}

	secret, key, err := apikey.Create(req.Name)
	if err != nil {
		if errors.Is(err, apikey.ErrNameTooLong) {
			rsp.ErrRsp(c, -1, "name is too long")
			return
		}

		rsp.ErrRsp(c, -2, "failed to create api key")
		return
	}

	// The secret is returned here and nowhere else. Only its digest is kept,
	// so it cannot be shown again.
	rsp.OkRspWithData(c, &proto.CreateAPIKeyRsp{
		ID:        key.ID,
		Name:      key.Name,
		CreatedAt: key.CreatedAt,
		Key:       secret,
	})

	log.Debugf("created api key %s", key.ID)
}

func (s *Service) GetAPIKeys(c *gin.Context) {
	var rsp proto.Response

	keys, err := apikey.List()
	if err != nil {
		rsp.ErrRsp(c, -2, "failed to read api keys")
		return
	}

	listed := make([]proto.APIKey, 0, len(keys))
	for _, key := range keys {
		listed = append(listed, proto.APIKey{
			ID:        key.ID,
			Name:      key.Name,
			CreatedAt: key.CreatedAt,
		})
	}

	rsp.OkRspWithData(c, &proto.GetAPIKeysRsp{Keys: listed})
}

func (s *Service) DeleteAPIKey(c *gin.Context) {
	var rsp proto.Response

	id := c.Param("id")
	if id == "" {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}

	if err := apikey.Revoke(id); err != nil {
		if errors.Is(err, apikey.ErrNotFound) {
			rsp.ErrRsp(c, -1, "api key not found")
			return
		}

		rsp.ErrRsp(c, -2, "failed to revoke api key")
		return
	}

	rsp.OkRsp(c)
	log.Debugf("revoked api key %s", id)
}
