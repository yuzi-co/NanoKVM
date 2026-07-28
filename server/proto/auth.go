package proto

type LoginReq struct {
	Username string `validate:"required"`
	Password string `validate:"required"`
}

type LoginRsp struct {
	Token string `json:"token"`
}

type GetAccountRsp struct {
	Username string `json:"username"`
}

type ChangePasswordReq struct {
	Username string `json:"username" validate:"required"`
	Password string `json:"password" validate:"required"`
	// OldPassword is required unless the device still uses the default account.
	OldPassword string `json:"oldPassword"`
}

type IsPasswordUpdatedRsp struct {
	IsUpdated bool `json:"isUpdated"`
}

type CreateAPIKeyReq struct {
	Name string `json:"name"`
}

// APIKey describes an issued key. The secret is deliberately absent: it is
// only ever returned by CreateAPIKeyRsp, at the moment it is issued.
type APIKey struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"createdAt"`
}

type CreateAPIKeyRsp struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"createdAt"`
	// Key is shown once and cannot be recovered afterwards.
	Key string `json:"key"`
}

type GetAPIKeysRsp struct {
	Keys []APIKey `json:"keys"`
}
