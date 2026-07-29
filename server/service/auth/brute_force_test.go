package auth

import (
	"fmt"
	"testing"
	"time"

	"NanoKVM-Server/config"
)

// useLockout turns brute-force protection on for the duration of a test and
// clears any state left behind by another one.
func useLockout(t *testing.T, maxFailures int) {
	t.Helper()

	conf := config.GetInstance()

	originalDuration := conf.Security.LoginLockoutDuration
	originalFailures := conf.Security.LoginMaxFailures

	conf.Security.LoginLockoutDuration = 600
	conf.Security.LoginMaxFailures = maxFailures

	t.Cleanup(func() {
		conf.Security.LoginLockoutDuration = originalDuration
		conf.Security.LoginMaxFailures = originalFailures

		loginMutex.Lock()
		defer loginMutex.Unlock()
		loginAttempts = make(map[string]*loginAttempt)
	})

	loginMutex.Lock()
	defer loginMutex.Unlock()
	loginAttempts = make(map[string]*loginAttempt)
}

func isLockedOut(ip string) bool {
	locked, _, _ := CheckLoginAttempt(ip)
	return locked
}

// An attacker who can reach the device from many addresses must not be able to
// wash out the record of an address that is already locked out.
func TestLockoutSurvivesRecordPressure(t *testing.T) {
	useLockout(t, 2)

	const victim = "192.0.2.10"

	RecordLoginFailure(victim)
	RecordLoginFailure(victim)

	if !isLockedOut(victim) {
		t.Fatal("victim should be locked out after reaching the failure limit")
	}

	for i := range maxLoginAttemptsRecords * 2 {
		RecordLoginFailure(fmt.Sprintf("198.51.100.%d", i))
	}

	if !isLockedOut(victim) {
		t.Fatal("lockout was lost while other addresses filled the table")
	}
}

func TestRecordTableStaysBounded(t *testing.T) {
	useLockout(t, 5)

	for i := range maxLoginAttemptsRecords * 2 {
		RecordLoginFailure(fmt.Sprintf("203.0.113.%d", i))
	}

	loginMutex.Lock()
	size := len(loginAttempts)
	loginMutex.Unlock()

	if size > maxLoginAttemptsRecords {
		t.Fatalf("table holds %d records, want at most %d", size, maxLoginAttemptsRecords)
	}
}

// Records for addresses that have gone quiet are the ones worth reclaiming.
func TestStaleRecordsAreReclaimedBeforeActiveOnes(t *testing.T) {
	useLockout(t, 5)

	const recent = "192.0.2.20"

	loginMutex.Lock()
	for i := range maxLoginAttemptsRecords {
		loginAttempts[fmt.Sprintf("198.51.100.%d", i)] = &loginAttempt{
			failures:   1,
			lastFailed: time.Now().Add(-24 * time.Hour),
		}
	}
	loginMutex.Unlock()

	RecordLoginFailure(recent)

	loginMutex.Lock()
	_, kept := loginAttempts[recent]
	loginMutex.Unlock()

	if !kept {
		t.Fatal("the new record was dropped instead of a stale one")
	}
}
