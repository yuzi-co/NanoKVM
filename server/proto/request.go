package proto

import (
	"os"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	log "github.com/sirupsen/logrus"
)

var env = os.Getenv(gin.EnvGinMode)

// validate is built once and reused: validator.New() allocates a fresh struct
// cache, so constructing it per request redoes the reflection work every time.
// The validator is documented as safe for concurrent use.
var validate = validator.New()

// ValidateRequest Validates request parameters.
func ValidateRequest(req interface{}) error {
	if err := validate.Struct(req); err != nil {
		log.Errorf("validate request failed, err: %s", err)
		return err
	}

	if env == "" || env == "debug" {
		log.Debugf("request: %+v\n", req)
	}

	return nil
}

// ParseQueryRequest Validates GET requests.
func ParseQueryRequest(c *gin.Context, req interface{}) error {
	var err error
	if err = c.ShouldBindQuery(req); err != nil {
		log.Errorf("parse request failed, err: %s", err)
		return err
	}

	return ValidateRequest(req)
}

// ParseFormRequest Validates POST Requests.
func ParseFormRequest(c *gin.Context, req interface{}) error {
	var err error
	if err = c.ShouldBind(req); err != nil {
		log.Errorf("parse request failed, err: %s", err)
		return err
	}

	return ValidateRequest(req)
}
