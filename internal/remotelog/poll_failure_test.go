package remotelog

import (
	"errors"
	"strings"
	"testing"

	"github.com/lich0821/ccNexus/internal/logger"
)

func TestPollFailureRecorderWarnsAfterThresholdAndLogsRecovery(t *testing.T) {
	log := logger.GetLogger()
	log.Clear()
	log.SetMinLevel(logger.DEBUG)
	log.SetConsoleLevel(logger.ERROR)
	t.Cleanup(func() {
		log.Clear()
		log.SetMinLevel(logger.DEBUG)
		log.SetConsoleLevel(logger.INFO)
	})

	recorder := NewPollFailureRecorder(3)
	timeoutErr := errors.New("timeout")

	recorder.Record(timeoutErr)
	recorder.Record(timeoutErr)
	recorder.Record(timeoutErr)
	recorder.Record(nil)
	recorder.Record(timeoutErr)

	entries := log.GetLogs()
	if len(entries) != 5 {
		t.Fatalf("log entries = %d, want 5: %#v", len(entries), entries)
	}
	wantLevels := []logger.LogLevel{logger.DEBUG, logger.DEBUG, logger.WARN, logger.INFO, logger.DEBUG}
	for i, want := range wantLevels {
		if entries[i].Level != want {
			t.Fatalf("entry %d level = %s, want %s (%#v)", i, entries[i].Level, want, entries)
		}
	}
	if !strings.Contains(entries[2].Message, "3 consecutive") {
		t.Fatalf("third failure message = %q, want consecutive warning", entries[2].Message)
	}
	if !strings.Contains(entries[3].Message, "recovered after 3 failures") {
		t.Fatalf("recovery message = %q", entries[3].Message)
	}
	if !strings.Contains(entries[4].Message, "(1/3)") {
		t.Fatalf("post-recovery failure message = %q, want reset counter", entries[4].Message)
	}
}

func TestPollFailureRecorderDoesNotLogRecoveryBelowThreshold(t *testing.T) {
	log := logger.GetLogger()
	log.Clear()
	log.SetMinLevel(logger.DEBUG)
	log.SetConsoleLevel(logger.ERROR)
	t.Cleanup(func() {
		log.Clear()
		log.SetMinLevel(logger.DEBUG)
		log.SetConsoleLevel(logger.INFO)
	})

	recorder := NewPollFailureRecorder(3)
	recorder.Record(errors.New("temporary timeout"))
	recorder.Record(nil)

	entries := log.GetLogs()
	if len(entries) != 1 {
		t.Fatalf("log entries = %d, want only debug failure: %#v", len(entries), entries)
	}
	if entries[0].Level != logger.DEBUG {
		t.Fatalf("entry level = %s, want DEBUG", entries[0].Level)
	}
}
