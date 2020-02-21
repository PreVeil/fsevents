/*
The Cache have RenameFrom : oldPath and RenameTo: newPath events cached.
The time that an event was added is kept in timers cache. It's circular array.
Every 100 miliseconds expired items are evicted from cache and if there is no match for them CREATE or DELETE event is created.


*/

package fsevents

import (
	"time"
)

const (
	MaxCapacity = 2000
)

type TimerCache struct {
	Values               []TimerCacheItem
	NumberOfItems        int
	Capacity             int
	TimerHead, TimerTail int
}
type Cache struct {
	RenameFrom map[uint64]Event
	RenameTo   map[uint64]Event
	timers     TimerCache

	ProcessedEvents chan []Event
	done            chan bool
}
type TimerCacheItem struct {
	T  time.Time
	ID uint64
}

func (tc *TimerCache) moveHead() {
	tc.TimerHead = (tc.TimerHead + 1) % tc.Capacity
	tc.NumberOfItems--
}
func (tc *TimerCache) Add(t time.Time, id uint64) {
	tc.Values[tc.TimerTail] = TimerCacheItem{t, id}
	tc.NumberOfItems++
}
func (tc *TimerCache) Next() {
	tc.TimerTail = (tc.TimerTail + 1) % tc.Capacity
}
func (tc *TimerCache) prev() {
	tc.TimerTail = (tc.TimerTail - 1) % tc.Capacity
}
func (tc *TimerCache) Head() TimerCacheItem {
	return tc.Values[tc.TimerHead]
}

func (c *Cache) createRenameEvent(oldNameEvent, newNameEvent Event, oldNameExist, newNameExist bool) {
	if !newNameExist && oldNameExist { //ITEM REMOVED
		oldNameEvent.Flags = ItemRemoved
		c.BroadcastRenameEvent(oldNameEvent)
		return
	} else if newNameExist && !oldNameExist { //ITEM CREATED
		newNameEvent.Flags = ItemCreated
	} else if newNameExist && oldNameExist { //RENAME WITH FULL INFO
		newNameEvent.OldPath = oldNameEvent.Path
	}
	c.BroadcastRenameEvent(newNameEvent)
}
func (c *Cache) CheckForMatch(eventId uint64) (event Event, matchedEvent Event, eventExist, matchExist bool, mode string) {
	if event, eventExist = c.RenameFrom[eventId]; eventExist {
		delete(c.RenameFrom, event.ID)
		matchedEvent, matchExist = c.RenameTo[event.ID+1]
		if matchExist {
			delete(c.RenameTo, matchedEvent.ID)
		}
		mode = "RENAME_FROM"
		return
	} else if event, eventExist = c.RenameTo[eventId]; eventExist {
		delete(c.RenameTo, event.ID)
		matchedEvent, matchExist = c.RenameFrom[event.ID-1]
		if matchExist {
			delete(c.RenameTo, matchedEvent.ID)
		}
		mode = "RENAME_TO"
		return
	}
	return
}
func (c *Cache) removeHead() {
	event, matchedEvent, eventExist, matchExist, mode := c.CheckForMatch(c.timers.Head().ID)
	if mode == "RENAME_TO" {
		c.createRenameEvent(matchedEvent, event, matchExist, eventExist)
		c.timers.moveHead()
	} else if mode == "RENAME_FROM" {
		c.createRenameEvent(event, matchedEvent, eventExist, matchExist)
		c.timers.moveHead()
	}
}
func (c *Cache) addToTimer(eventId uint64) {
	for c.timers.NumberOfItems >= c.timers.Capacity {
		c.removeHead()
	}
	c.timers.Add(time.Now(), eventId)
	c.timers.Next()
}
func (c *Cache) timeDifference(t time.Time) int {
	currentTime := time.Now()
	difference := currentTime.Sub(t)
	total := int(difference.Milliseconds())
	return total
}
func (c *Cache) EventExists(eventId uint64) bool {
	//TODO maybe return the event
	if _, exist := c.RenameFrom[eventId]; exist {
		return true
	} else if _, exist := c.RenameTo[eventId]; exist {
		return true
	}
	return false
}
func (c *Cache) Add(e Event, mode string) {
	eventId := e.ID
	if mode == "RENAME_TO" {
		if renameFromEvent, exist := c.RenameFrom[eventId-1]; exist {
			c.createRenameEvent(renameFromEvent, e, exist, true)
			delete(c.RenameFrom, renameFromEvent.ID)
		} else {
			c.RenameTo[e.ID] = e
			c.addToTimer(e.ID)
		}
	} else {
		if renameToEvent, exist := c.RenameTo[eventId+1]; exist {
			c.createRenameEvent(e, renameToEvent, true, exist)
			delete(c.RenameTo, renameToEvent.ID)
		} else {
			c.RenameFrom[e.ID] = e
			c.addToTimer(e.ID)
		}
	}
}
func (c *Cache) getEvent(eventId uint64, mode string) (reqEvent Event, exist bool) {

	if mode == "RENAME_TO" {
		if reqEvent, exist = c.RenameTo[eventId]; exist {
			delete(c.RenameTo, reqEvent.ID)
		}
		return
	} else {
		if reqEvent, exist = c.RenameFrom[eventId]; exist {
			delete(c.RenameFrom, reqEvent.ID)
		}
		return
	}
}
func (c *Cache) findExpiredEvents() {
	itemRemoved := true
	for c.timers.NumberOfItems > 0 && itemRemoved {
		if c.timeDifference(c.timers.Head().T)*1000 > int(1*time.Millisecond) {
			if c.EventExists(c.timers.Head().ID) {
				event, matchedEvent, eventExist, matchExist, eventType := c.CheckForMatch(c.timers.Head().ID)
				if eventType == "RENAME_TO" {
					c.createRenameEvent(matchedEvent, event, matchExist, eventExist)
				} else {
					c.createRenameEvent(event, matchedEvent, eventExist, matchExist)
				}
			}
			c.timers.moveHead()
		} else if !c.EventExists(c.timers.Head().ID) {
			c.timers.moveHead()
		} else {
			itemRemoved = false //break the loop if the head has not changed
		}
	}
}

func (c *Cache) BroadcastRenameEvent(e Event) { // suggest a better way of doing this
	events := make([]Event, 1)
	events[0] = e
	c.ProcessedEvents <- events
}

func (c *Cache) Start(es *EventStream) {
	c.RenameFrom = make(map[uint64]Event)
	c.RenameTo = make(map[uint64]Event)

	c.timers = TimerCache{make([]TimerCacheItem, MaxCapacity), 0, MaxCapacity, 0, 0}
	c.Events = es.Events

	ticker := time.NewTicker(100 * time.Millisecond)
	c.done = make(chan bool)
	go func() {
		for {
			select {
			case <-c.done:
				return
			case <-ticker.C:
				c.findExpiredEvents()
			}
		}
	}()
}
