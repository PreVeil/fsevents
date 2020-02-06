// +build darwin

package fsevents

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"
	"sync/atomic"
	"runtime"
	"os/exec"
	"strconv"
)
type counter struct {
	val int32
}

func (c *counter) increment() {
	atomic.AddInt32(&c.val, 1)
}

func (c *counter) value() int32 {
	return atomic.LoadInt32(&c.val)
}

func (c *counter) reset() {
	atomic.StoreInt32(&c.val, 0)
}


func TestBasicExample(t *testing.T) {
	path, err := ioutil.TempDir("", "fsexample")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(path)

	dev, err := DeviceForPath(path)
	if err != nil {
		t.Fatal(err)
	}

	es := &EventStream{
		Paths:   []string{path},
		Latency: 500 * time.Millisecond,
		Device:  dev,
		Flags:   FileEvents,
	}

	es.Start()

	wait := make(chan Event)
	go func() {
		for msg := range es.Events {
			for _, event := range msg {
				t.Logf("Event: %#v", event)
				wait <- event
				es.Stop()
				return
			}
		}
	}()

	err = ioutil.WriteFile(filepath.Join(path, "example.txt"), []byte("example"), 0700)
	if err != nil {
		t.Fatal(err)
	}

	<-wait
}
// creates a file, modifies it, rename it, delete it! the first 3 events should be
func TestFseventRename(t *testing.T) {
	path, err := ioutil.TempDir("", "fsexample")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(path)

	dev, err := DeviceForPath(path)
	if err != nil {
		t.Fatal(err)
	}

	es := &EventStream{
		Paths:   []string{path},
		Latency: 500 * time.Millisecond,
		Device:  dev,
		Flags:   FileEvents,
	}

	es.Start()
	defer es.Stop()
	
	var renameReceived counter
	done := make(chan bool)
	wait := make(chan bool)
	testFile := filepath.Join(path, "TestFseventEvents.testfile")
	testFileRenamed := filepath.Join(path, "TestFseventEvents.testfileRenamed")

	go func() {
		for {
			select{
			case _ = <- wait:
				done <- true
				return
			case msg := <- es.Events:
				for _, event := range msg {
					if event.Flags&ItemRenamed==ItemRenamed{
						renameReceived.increment()
					}
					t.Logf("Event: %#v", event)
				}
			}
		}
	}()
	f, err := os.OpenFile(testFile, os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		t.Fatalf("creating test file failed: %s", err)
	}
	f.Sync()

	f.WriteString("data")
	f.Sync()
	f.Close()

	if err := testRename(testFile, testFileRenamed); err != nil {
		t.Fatalf("rename failed: %s", err)
	}
	// We expect this event to be received almost immediately, but let's wait 500 ms to be sure
	time.Sleep(700 * time.Millisecond)
	if renameReceived.value() == 0 {
		t.Fatal("fsnotify rename events have not been received after 500 ms")
	}
	// Try closing the fsnotify instance
	wait <- true
	t.Log("waiting for the event channel to become closed...")
	select {
	case <-done:
		t.Log("event channel closed")
	case <-time.After(2 * time.Second):
		t.Fatal("event stream was not closed after 2 seconds")
	}
	os.Remove(testFileRenamed)
}

func TestFseventRenameEventAttributes(t *testing.T) {
	path, err := ioutil.TempDir("", "fsexample")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(path)

	dev, err := DeviceForPath(path)
	if err != nil {
		t.Fatal(err)
	}

	es := &EventStream{
		Paths:   []string{path},
		Latency: 500 * time.Millisecond,
		Device:  dev,
		Flags:   FileEvents,
	}

	es.Start()
	defer es.Stop()
	
	var renameReceived counter
	done := make(chan bool)
	wait := make(chan bool)
	testFile := filepath.Join(path, "TestFseventEvents.testfile")
	testFileRenamed := filepath.Join(path, "TestFseventEvents.testfileRenamed")

	go func() {
		for {
			select{
			case _ = <- wait:
				done <- true
				return
			case msg := <- es.Events:
				for _, event := range msg {
					if event.Flags&ItemRenamed==ItemRenamed{
						if event.Path == filepath.Clean(testFile) {
							if event.OldPath != filepath.Clean(testFileRenamed) {
								t.Logf("unexpected old name for a file in rename event: %#v", event)
							}
						}
						renameReceived.increment()
					}
					t.Logf("Event: %#v", event)
				}
			}
		}
	}()
	f, err := os.OpenFile(testFile, os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		t.Fatalf("creating test file failed: %s", err)
	}
	f.Close()

	if err := testRename(testFile, testFileRenamed); err != nil {
		t.Fatalf("rename failed: %s", err)
	}
	// We expect this event to be received almost immediately, but let's wait 500 ms to be sure
	time.Sleep(700 * time.Millisecond)
	if renameReceived.value() == 0 {
		t.Fatal("fsnotify rename events have not been received after 500 ms")
	}
	// Try closing the fsnotify instance
	wait <- true
	t.Log("waiting for the event channel to become closed...")
	select {
	case <-done:
		t.Log("event channel closed")
	case <-time.After(2 * time.Second):
		t.Fatal("event stream was not closed after 2 seconds")
	}
	os.Remove(testFileRenamed)
}

func TestFseventMultipleRenames(t *testing.T) {
	path, err := ioutil.TempDir("", "fsexample")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(path)

	dev, err := DeviceForPath(path)
	if err != nil {
		t.Fatal(err)
	}

	es := &EventStream{
		Paths:   []string{path},
		Latency: 500 * time.Millisecond,
		Device:  dev,
		Flags:   FileEvents,
	}

	es.Start()
	defer es.Stop()
	
	var renameReceived counter
	done := make(chan bool)
	wait := make(chan bool)
	// testFile := filepath.Join(path, "TestFseventEvents.testfile")
	// testFileRenamed := filepath.Join(path, "TestFseventEvents.testfileRenamed")

	newName := ""
	oldName := ""

	go func() {
		for {
			select{
			case _ = <- wait:
				done <- true
				return
			case msg := <- es.Events:
				for _, event := range msg {
					if event.Flags&ItemRenamed==ItemRenamed{
						newName, _ = filepath.Rel(path, event.Path)
						oldName, _ = filepath.Rel(path, event.OldPath)
						if newName != oldName+"Renamed"{
							t.Errorf("rename order messed up: %#v", event)
						}
						renameReceived.increment()
					}
					t.Logf("Event: %#v", event)
				}
			}
		}
	}()

	numberOfFiles :=1000
	testFileNameCommon := "TestFseventEvents.testfile"
	renameAction := func(t *testing.T, count string){
		testFile := filepath.Join(path, testFileNameCommon+count)
		f, err := os.OpenFile(testFile, os.O_WRONLY|os.O_CREATE, 0666)
		if err != nil {
			t.Fatalf("creating test file failed: %s", err)
		}
		f.Close()

		testFileRenamed := filepath.Join(path, testFileNameCommon+count+"Renamed")
		if err := testRename(testFile, testFileRenamed); err != nil{
			t.Fatalf("rename action failed (SYSCALL): %s", err)
		}
	}

	for i := 0; i < numberOfFiles; i++ {
		go renameAction(t, strconv.Itoa(i))
		time.Sleep(5 * time.Millisecond)
	}

	// We expect this event to be received almost immediately, but let's wait 500 ms to be sure
	time.Sleep(1000 * time.Millisecond)
	if renameReceived.value() == 0 {
		t.Fatal("fsnotify rename events have not been received after 500 ms")
	}
	t.Logf("Event: %#v", renameReceived.value())
	if renameReceived.value() != int32(numberOfFiles) {
		t.Fatal("watcher missed a fsevent rename event")
	}
	// Try closing the fsnotify instance
	wait <- true
	t.Log("waiting for the event channel to become closed...")
	select {
	case <-done:
		t.Log("event channel closed")
	case <-time.After(2 * time.Second):
		t.Fatal("event stream was not closed after 2 seconds")
	}
	for i := 0; i < numberOfFiles; i++ {
		testFileRenamed := filepath.Join(path, testFileNameCommon+strconv.Itoa(i)+"Renamed")
		os.Remove(testFileRenamed)
	}

}

func testRename(file1, file2 string) error {
	switch runtime.GOOS {
	case "windows", "plan9":
		return os.Rename(file1, file2)
	default:
		cmd := exec.Command("mv", file1, file2)
		return cmd.Run()
	}
}