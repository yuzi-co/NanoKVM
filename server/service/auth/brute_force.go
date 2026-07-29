package auth

import (
	"sync"
	"time"

	"NanoKVM-Server/config"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

type loginAttempt struct {
	failures   int
	lastFailed time.Time
	lockoutEnd time.Time
}

const (
	maxLoginAttemptsRecords = 3000
	cleanupInterval         = 6 * time.Hour

	// staleRecordWindow is how long a record with no lockout on it is kept
	// after its last failure.
	staleRecordWindow = 30 * time.Minute
)

var (
	loginAttempts = make(map[string]*loginAttempt)
	loginMutex    sync.Mutex
	cleanupOnce   sync.Once
)

// startCleanupRoutine starts a background routine to clean up memory
func startCleanupRoutine() {
	conf := config.GetInstance()
	if conf.Security.LoginLockoutDuration <= 0 {
		return
	}

	go func() {
		ticker := time.NewTicker(cleanupInterval)
		for range ticker.C {
			loginMutex.Lock()
			now := time.Now()
			for ip, attempt := range loginAttempts {
				if isExpiredLocked(attempt, now) {
					delete(loginAttempts, ip)
				}
			}
			loginMutex.Unlock()
		}
	}()
}

// GetClientIP gets a reliable real IP
func GetClientIP(c *gin.Context) string {
	ip := c.RemoteIP()
	if ip == "" {
		ip = c.ClientIP()
	}
	return ip
}

// CheckLoginAttempt checks if a login attempt is allowed based on brute-force protection rules.
// Returning true means the IP/System is locked out, and an error string and error code are returned.
func CheckLoginAttempt(clientIP string) (bool, int, string) {
	conf := config.GetInstance()
	if conf.Security.LoginLockoutDuration <= 0 {
		return false, 0, ""
	}

	cleanupOnce.Do(startCleanupRoutine)

	loginMutex.Lock()
	defer loginMutex.Unlock()

	if attempt, exists := loginAttempts[clientIP]; exists {
		if time.Now().Before(attempt.lockoutEnd) {
			log.Debugf("login blocked for IP %s: account locked due to too many failed attempts (until %s)", clientIP, attempt.lockoutEnd)
			return true, -5, "Account locked due to too many failed attempts, please try again later"
		}

		// If lockout has elapsed, then we reset the failures and lockoutEnd.
		if !attempt.lockoutEnd.IsZero() {
			attempt.failures = 0
			attempt.lockoutEnd = time.Time{}
		}
	}

	return false, 0, ""
}

// RecordLoginFailure records a failed login attempt for the given IP address.
func RecordLoginFailure(clientIP string) (bool, int, string) {
	conf := config.GetInstance()
	if conf.Security.LoginLockoutDuration <= 0 {
		return false, 0, ""
	}

	cleanupOnce.Do(startCleanupRoutine)

	loginMutex.Lock()
	defer loginMutex.Unlock()

	attempt, exists := loginAttempts[clientIP]
	if !exists {
		// Emptying the table here would hand every locked-out address a clean
		// slate, so an attacker with a range to spend could stay ahead of the
		// limit forever by flushing it. Reclaim one record instead, and pick
		// the one worth the least.
		if len(loginAttempts) >= maxLoginAttemptsRecords && !evictOneRecordLocked() {
			log.Warn("login attempt records are full and every record is live; not tracking this address")
			return false, 0, ""
		}

		attempt = &loginAttempt{}
		loginAttempts[clientIP] = attempt
	}

	now := time.Now()
	// Failure time window: if it has been a long time since the last failure
	// (e.g., beyond the lockoutDuration window), reset the failure count
	if !attempt.lastFailed.IsZero() && now.Sub(attempt.lastFailed) > time.Duration(conf.Security.LoginLockoutDuration)*time.Second {
		attempt.failures = 0
	}

	attempt.failures++
	attempt.lastFailed = now

	// Reach the failure limit, lock out
	if attempt.failures >= conf.Security.LoginMaxFailures {
		attempt.lockoutEnd = now.Add(time.Duration(conf.Security.LoginLockoutDuration) * time.Second)
		log.Debugf("login failures reached threshold for IP %s, locking out until %s", clientIP, attempt.lockoutEnd)
	}

	return false, 0, ""
}

// evictOneRecordLocked frees a slot in the record table, reporting whether it
// managed to. Records are given up in order of how little they are still worth:
// ones that have expired, then ones that are merely counting failures, and a
// live lockout only when there is nothing else left.
func evictOneRecordLocked() bool {
	now := time.Now()

	var (
		oldestUnlockedIP string
		oldestUnlocked   time.Time

		soonestLockoutIP string
		soonestLockout   time.Time
	)

	// One pass, reclaiming every record that has expired rather than stopping
	// at the first: the scan costs the same either way, and taking them all
	// keeps the next few insertions from repeating it.
	reclaimed := false

	for ip, attempt := range loginAttempts {
		if isExpiredLocked(attempt, now) {
			delete(loginAttempts, ip)
			reclaimed = true

			continue
		}

		if attempt.lockoutEnd.IsZero() {
			if oldestUnlockedIP == "" || attempt.lastFailed.Before(oldestUnlocked) {
				oldestUnlockedIP = ip
				oldestUnlocked = attempt.lastFailed
			}

			continue
		}

		if soonestLockoutIP == "" || attempt.lockoutEnd.Before(soonestLockout) {
			soonestLockoutIP = ip
			soonestLockout = attempt.lockoutEnd
		}
	}

	if reclaimed {
		return true
	}

	if oldestUnlockedIP != "" {
		delete(loginAttempts, oldestUnlockedIP)
		return true
	}

	if soonestLockoutIP != "" {
		delete(loginAttempts, soonestLockoutIP)
		return true
	}

	return false
}

// isExpiredLocked reports whether a record has outlived its usefulness: either
// its lockout has run out, or it never had one and has gone quiet.
func isExpiredLocked(attempt *loginAttempt, now time.Time) bool {
	if !attempt.lockoutEnd.IsZero() {
		return now.After(attempt.lockoutEnd)
	}

	return now.Sub(attempt.lastFailed) > staleRecordWindow
}

// ClearLoginAttempt clears the failed login attempt record for an IP upon successful login.
func ClearLoginAttempt(clientIP string) {
	conf := config.GetInstance()
	if conf.Security.LoginLockoutDuration <= 0 {
		return
	}

	loginMutex.Lock()
	defer loginMutex.Unlock()

	delete(loginAttempts, clientIP)
}
