// +build darwin

package main

import (
	"bufio"
	"io/ioutil"
	"log"
	"os"
	"runtime"
	"time"

	"github.com/fsnotify/fsevents"
)

func main() {
	path, err := ioutil.TempDir("", "fsexample")
	if err != nil {
		log.Fatalf("Failed to create TempDir: %v", err)
	}
	dev, err := fsevents.DeviceForPath(path)
	if err != nil {
		log.Fatalf("Failed to retrieve device for path: %v", err)
	}
	log.Print(dev)
	log.Println(fsevents.EventIDForDeviceBeforeTime(dev, time.Now()))

	es := &fsevents.EventStream{
		Paths:   []string{path},
		Latency: 500 * time.Millisecond,
		Device:  dev,
		Flags:   fsevents.FileEvents | fsevents.WatchRoot}
	es.Start()
	ec := es.Events

	log.Println("Device UUID", fsevents.GetDeviceUUID(dev))

	go func() {
		for msg := range ec {
			for _, event := range msg {
				logEvent(event)
			}
		}
	}()

	in := bufio.NewReader(os.Stdin)

	if false {
		log.Print("Started, press enter to GC")
		in.ReadString('\n')
		runtime.GC()
		log.Print("GC'd, press enter to quit")
		in.ReadString('\n')
	} else {
		log.Print("Started, press enter to stop")
		in.ReadString('\n')
		es.Stop()

		log.Print("Stopped, press enter to restart")
		in.ReadString('\n')
		es.Resume = true
		es.Start()

		log.Print("Restarted, press enter to quit")
		in.ReadString('\n')
		es.Stop()
	}
}

func logEvent(event fsevents.Event) {
	log.Printf(event.String())
}
