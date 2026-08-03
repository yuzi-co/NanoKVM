package logger

import (
	"testing"

	"github.com/sirupsen/logrus"
)

func TestCallerReportingIsOffForCoarseLevels(t *testing.T) {
	// Caller reporting walks the stack for every entry that passes the level
	// filter. On the production levels the file and line add nothing.
	for _, level := range []logrus.Level{logrus.ErrorLevel, logrus.WarnLevel, logrus.InfoLevel} {
		if shouldReportCaller(level) {
			t.Fatalf("expected caller reporting to be off at %s", level)
		}
	}
}

func TestCallerReportingIsOnWhenDebugging(t *testing.T) {
	for _, level := range []logrus.Level{logrus.DebugLevel, logrus.TraceLevel} {
		if !shouldReportCaller(level) {
			t.Fatalf("expected caller reporting to be on at %s", level)
		}
	}
}
