package config

type Config struct {
	Proto          string   `yaml:"proto"`
	Host           string   `yaml:"host"`
	Port           Port     `yaml:"port"`
	Cert           Cert     `yaml:"cert"`
	Logger         Logger   `yaml:"logger"`
	Authentication string   `yaml:"authentication"`
	JWT            JWT      `yaml:"jwt"`
	Stun           string   `yaml:"stun"`
	Turn           Turn     `yaml:"turn"`
	Security       Security `yaml:"security"`
	Ion            Ion      `yaml:"ion"`
	// Proxy reaches the internet through an intermediary. A complete URL or a
	// bare host:port. Empty means the environment decides.
	Proxy string `yaml:"proxy"`

	Hardware Hardware `yaml:"-"`
}

type Logger struct {
	Level string `yaml:"level"`
	File  string `yaml:"file"`
}

type Port struct {
	Http  int `yaml:"http"`
	Https int `yaml:"https"`
}

type Cert struct {
	Crt string `yaml:"crt"`
	Key string `yaml:"key"`
}

type JWT struct {
	SecretKey            string `yaml:"secretKey"`
	RefreshTokenDuration uint64 `yaml:"refreshTokenDuration"`
	RevokeTokensOnLogout bool   `yaml:"revokeTokensOnLogout"`
}

type Turn struct {
	TurnAddr string `yaml:"turnAddr"`
	TurnUser string `yaml:"turnUser"`
	TurnCred string `yaml:"turnCred"`
}

type Security struct {
	LoginLockoutDuration int `yaml:"loginLockoutDuration"`
	LoginMaxFailures     int `yaml:"loginMaxFailures"`
}

// Ion configures how the carveout is graded.
type Ion struct {
	// ReserveFloor is the assumed cost of starting the stream, in bytes, used
	// until this process has been observed needing more. 12MB: opening a stream
	// on a fresh board took the carveout from 19,050,496 to 30,392,320, so one
	// stream start costs 11,341,824 bytes.
	//
	// An earlier value of 24MB was the cost of a whole capture session, which
	// graded a healthy board amber: `ok` needs twice this much free, and twice
	// 24MB is 64% of the 75MB carveout, so a board with video running could
	// never reach it.
	ReserveFloor uint64 `yaml:"reserveFloor"`
}

type Hardware struct {
	Version      HWVersion `yaml:"-"`
	GPIOReset    string    `yaml:"-"`
	GPIOPower    string    `yaml:"-"`
	GPIOPowerLED string    `yaml:"-"`
	GPIOHDDLed   string    `yaml:"-"`
}
