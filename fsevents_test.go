//go:build darwin
// +build darwin

package fsevents

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
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

func generateTempDir(t *testing.T) string {
	path, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	randStr, err := randString(6)
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := filepath.Join(path, randStr)
	err = os.MkdirAll(tmpDir, 0700)
	if err != nil {
		t.Fatal(err)
	}
	return tmpDir
}

func randString(bytes int) (string, error) {
	buf := make([]byte, bytes)
	_, err := rand.Read(buf)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:])[:len(buf)], nil
}

func TestBasicExample(t *testing.T) {
	path := generateTempDir(t)
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

	err = es.Start()
	if err != nil {
		t.Fatal(err)
	}

	wait := make(chan Event)
	go func() {
		for msg := range es.Events {
			for _, event := range msg {
				t.Logf("Event: %s", event)
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
	select {
	case <-wait:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestIssue48(t *testing.T) {
	// FSEvents fails to start when watching >4096 paths
	// This test validates that limit and checks that the error is propagated

	path, err := ioutil.TempDir("", "fsmanyfiles")
	if err != nil {
		t.Fatal(err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(path)

	// TODO: using this value fails to start
	// dev, err := DeviceForPath(path)
	// if err != nil {
	// 	t.Fatal(err)
	// }

	var filenames []string
	for i := 0; i < 4096; i++ {
		newFilename := filepath.Join(path, fmt.Sprint("test", i))
		err = ioutil.WriteFile(newFilename, []byte("test"), 0700)
		if err != nil {
			t.Fatal(err)
		}
		filenames = append(filenames, newFilename)
	}

	es := &EventStream{
		Paths:   filenames,
		Latency: 500 * time.Millisecond,
		Device:  0, //dev,
		Flags:   FileEvents,
	}

	err = es.Start()
	if err != nil {
		t.Fatal(err)
	}

	wait := make(chan Event)
	go func() {
		for msg := range es.Events {
			for _, event := range msg {
				t.Logf("Event: %#v", event)
				es.Stop()
				wait <- event
				return
			}
		}
	}()

	// write some new contents to test42 in the watchlist
	err = ioutil.WriteFile(filenames[42], []byte("special"), 0700)
	if err != nil {
		t.Fatal(err)
	}

	// should be reported as expected
	<-wait

	/////
	// create one more file that puts it over the edge
	newFilename := filepath.Join(path, fmt.Sprint("test", 4096))
	err = ioutil.WriteFile(newFilename, []byte("test"), 0700)
	if err != nil {
		t.Fatal(err)
	}
	filenames = append(filenames, newFilename)

	// create an all-new instances to avoid problems
	es = &EventStream{
		Paths:   filenames,
		Latency: 500 * time.Millisecond,
		Device:  0, //dev,
		Flags:   FileEvents,
	}

	err = es.Start()
	if err == nil {
		es.Stop()
		t.Fatal("eventstream error was not detected on >4096 files in watchlist")
	}
}

func TestRegistry(t *testing.T) {
	if registry.m == nil {
		t.Fatal("registry not initialized at start")
	}

	es := &EventStream{}
	i := registry.Add(es)

	if registry.Get(i) == nil {
		t.Fatal("failed to retrieve es from registry")
	}

	if es != registry.Get(i) {
		t.Errorf("eventstream did not match what was found in the registry")
	}

	registry.Delete(i)
	if registry.Get(i) != nil {
		t.Error("failed to delete registry")
	}
}

func TestFSEventBasicRename(t *testing.T) {
	path, err := ioutil.TempDir("", "this also works test dir")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(path)

	es := &EventStream{
		Paths:   []string{path},
		Latency: 500 * time.Millisecond,
		Flags:   FileEvents,
	}

	es.Start()
	defer es.Stop()

	var renameReceived counter
	wait := make(chan bool)
	testFile := filepath.Join(path, "TestFseventEvents.testfile")
	testFileRenamed := filepath.Join(path, "TestFseventEvents.testfileRenamed")
	go func() {
		for {
			select {
			case <-wait:
				return
			case msg := <-es.Events:
				for _, event := range msg {
					if event.Flags&ItemRenamed == ItemRenamed {
						if event.Path == testFile {
							if event.OldPath != testFileRenamed {
								t.Logf("unexpected old name for a file in rename event: %#v", event)
							}
						}
						renameReceived.increment()
					}
				}
			}
		}
	}()

	f, err := os.OpenFile(testFile, os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		t.Fatalf("creating test file failed: %s", err)
	}
	f.WriteString("data")
	f.Sync()
	f.Close()

	if err := testRename(testFile, testFileRenamed); err != nil {
		t.Fatalf("rename failed: %s", err)
	}
	time.Sleep(700 * time.Millisecond)
	if renameReceived.value() != 1 {
		t.Fatal("the expected rename events have not been received after 500 ms")
	}
	close(wait)
}

func TestFSEventMultipleRenames(t *testing.T) {
	path := generateTempDir(t)
	defer os.RemoveAll(path)

	es := &EventStream{
		Paths:   []string{path},
		Latency: 500 * time.Millisecond,
		Flags:   FileEvents,
	}
	es.Start()
	defer es.Stop()

	var renameReceived counter
	wait := make(chan bool)
	go func() {
		for {
			select {
			case <-wait:
				return
			case msg := <-es.Events:
				for _, event := range msg {
					if event.Flags&ItemRenamed == ItemRenamed {
						if filepath.Base(event.Path) == filepath.Join(filepath.Base(event.OldPath), "Renamed") {
							t.Errorf("rename order messed up: %#v", event)
						}
						renameReceived.increment()
						t.Logf("Rename event received: %s", event)
					}
				}
			}
		}
	}()

	testFileNameCommon := "FSEvents.testfile"
	errs := make(chan error)
	defer close(errs)
	renameAction := func(count string) {
		testFile := filepath.Join(path, testFileNameCommon+count)
		f, err := os.OpenFile(testFile, os.O_WRONLY|os.O_CREATE, 0666)
		if err != nil {
			errs <- err
		}
		f.Close()
		testFileRenamed := filepath.Join(path, testFileNameCommon+count+"Renamed")
		if err := testRename(testFile, testFileRenamed); err != nil {
			errs <- err
		}
	}

	numberOfFiles := 3
	for i := 0; i < numberOfFiles; i++ {
		go renameAction(strconv.Itoa(i))
	}

	time.Sleep(1000 * time.Millisecond)

	select {
	case err := <-errs:
		t.Fatal(err)
	default:
	}

	if renameReceived.value() != int32(numberOfFiles) {
		t.Fatal("missing fsevents rename events")
	}
	close(wait)
}

func testRename(file1, file2 string) error {
	switch runtime.GOOS {
	case "windows", "plan9":
		return os.Rename(file1, file2)
	default:
		return exec.Command("mv", file1, file2).Run()
	}
}
