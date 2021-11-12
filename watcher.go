package webserver

import (
	"log"
	"os"
	"time"

	cache "github.com/patrickmn/go-cache"
	"github.com/rjeczalik/notify"
)

var WatcherNotifies = []notify.Event{notify.All}

func NewFileWatcher(path string, fn func(ev string, path string)) {

	go func() {
		c := make(chan notify.EventInfo, 1)
		err := notify.Watch(path+"/...", c, WatcherNotifies...)

		if err != nil {
			log.Fatal(err)
		}
		defer notify.Stop(c)

		timeCache := cache.New(5*time.Minute, 30*time.Second)

		for {
			// Block until an event is received.
			ei := <-c
			eventPath := ei.Path()
			st, err := os.Stat(eventPath)
			if err != nil {
				timeCache.Delete(eventPath)
				continue
			}

			modTime := st.ModTime()
			isEnable := true
			if v, has := timeCache.Get(eventPath); has {
				if oldTime, is := v.(time.Time); is {
					if oldTime.Equal(modTime) {
						isEnable = false
					}
				}
			}
			if isEnable {
				fn(ei.Event().String()[7:], eventPath)
			}
			timeCache.Set(eventPath, modTime, cache.DefaultExpiration)
		}
	}()
}
