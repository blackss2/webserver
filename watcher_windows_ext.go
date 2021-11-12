//go:build windows
// +build windows

package webserver

import (
	"github.com/rjeczalik/notify"
)

func init() {
	WatcherNotifies = []notify.Event{notify.All, notify.FileNotifyChangeLastWrite}
}
