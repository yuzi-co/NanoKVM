package auth

import (
	"NanoKVM-Server/utils"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	log "github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"
)

// AccountFile is a variable rather than a constant so tests can redirect it.
var AccountFile = "/etc/kvm/pwd"

type Account struct {
	Username string `json:"username"`
	Password string `json:"password"` // should be named HashedPassword for clarity
}

func GetAccount() (*Account, error) {
	if _, err := os.Stat(AccountFile); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return getDefaultAccount(), nil
		}
		return nil, err
	}

	content, err := os.ReadFile(AccountFile)
	if err != nil {
		return nil, err
	}

	var account Account
	if err = json.Unmarshal(content, &account); err != nil {
		log.Errorf("unmarshal account failed: %s", err)
		return nil, err
	}

	return &account, nil
}

// isAccountConfigured reports whether a password has ever been set on this
// device. Without the file, GetAccount falls back to the admin/admin default.
func isAccountConfigured() bool {
	_, err := os.Stat(AccountFile)
	return err == nil
}

func SetAccount(username string, hashedPassword string) error {
	account, err := json.Marshal(&Account{
		Username: username,
		Password: hashedPassword,
	})
	if err != nil {
		log.Errorf("failed to marshal account information to json: %s", err)
		return err
	}

	// 0o644 on a directory leaves it without the execute bit, so nothing
	// inside can be opened by path.
	err = os.MkdirAll(filepath.Dir(AccountFile), 0o755)
	if err != nil {
		log.Errorf("create directory %s failed: %s", AccountFile, err)
		return err
	}

	// The file holds a password hash; only root needs to read it.
	err = os.WriteFile(AccountFile, account, 0o600)
	if err != nil {
		log.Errorf("write password failed: %s", err)
		return err
	}

	return nil
}

func CompareAccount(username string, plainPassword string) bool {
	account, err := GetAccount()
	if err != nil {
		return false
	}

	if username != account.Username {
		return false
	}

	hashedPassword, err := utils.DecodeDecrypt(plainPassword)
	if err != nil || hashedPassword == "" {
		return false
	}

	err = bcrypt.CompareHashAndPassword([]byte(account.Password), []byte(hashedPassword))
	if err != nil {
		// Compatible with old versions
		accountHashedPassword, _ := utils.DecodeDecrypt(account.Password)
		if accountHashedPassword == hashedPassword {
			return true
		}

		return false
	}

	return true
}

func DelAccount() error {
	if err := os.Remove(AccountFile); err != nil {
		log.Errorf("failed to delete password: %s", err)
		return err
	}

	return nil
}

// defaultPasswordHash is derived once. A device that has never had a password
// set falls back to the default account on every login attempt, and bcrypt at
// the default cost is a second of a 1GHz C906 - paid on the exact path someone
// guessing passwords would be hammering, on top of the comparison itself.
var defaultPasswordHash = sync.OnceValue(func() string {
	hashed, err := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.DefaultCost)
	if err != nil {
		// Only possible if the cost is out of range, which it is not.
		log.Errorf("failed to hash the default password: %s", err)
		return ""
	}

	return string(hashed)
})

func getDefaultAccount() *Account {
	return &Account{
		Username: "admin",
		Password: defaultPasswordHash(),
	}
}
