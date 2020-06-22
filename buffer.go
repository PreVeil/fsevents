/*
The Cache have RenameFrom : oldPath and RenameTo: newPath events cached.
The time that an event was added is kept in timers cache. It's circular array.
Every 100 miliseconds expired items are evicted from cache and if there is no match for them CREATE or DELETE event is created.


*/

package fsevents

import (
	"sync"
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

	cacheLock sync.RWMutex

	ProcessedEvents chan []Event
	done            chan bool
}
type TimerCacheItem struct {
	T  time.Time
	ID uint64
}

func (tc *TimerCache) moveHead() {
	if tc.NumberOfItems > 0 {
		tc.TimerHead = (tc.TimerHead + 1) % tc.Capacity
		tc.NumberOfItems--
	}
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
	c.cacheLock.Lock()
	defer c.cacheLock.Unlock()
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
	c.cacheLock.RLock()
	defer c.cacheLock.RUnlock()
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
		c.cacheLock.RLock()
		renameFromEvent, exist := c.RenameFrom[eventId-1]
		c.cacheLock.RUnlock()

		if exist {
			c.createRenameEvent(renameFromEvent, e, exist, true)
			c.cacheLock.Lock()
			delete(c.RenameFrom, renameFromEvent.ID)
			c.cacheLock.Unlock()
		} else {
			c.cacheLock.Lock()
			c.RenameTo[e.ID] = e
			c.addToTimer(e.ID)
			c.cacheLock.Unlock()
		}
	} else {
		c.cacheLock.RLock()
		renameToEvent, exist := c.RenameTo[eventId+1]
		c.cacheLock.RUnlock()

		if exist {
			c.createRenameEvent(e, renameToEvent, true, exist)
			c.cacheLock.Lock()
			delete(c.RenameTo, renameToEvent.ID)
			c.cacheLock.Unlock()
		} else {
			c.cacheLock.Lock()
			c.RenameFrom[e.ID] = e
			c.addToTimer(e.ID)
			c.cacheLock.Unlock()
		}
	}
}
func (c *Cache) getEvent(eventId uint64, mode string) (reqEvent Event, exist bool) {
	if mode == "RENAME_TO" {
		if reqEvent, exist = c.RenameTo[eventId]; exist {
			c.cacheLock.Lock()
			delete(c.RenameTo, reqEvent.ID)
			c.cacheLock.Unlock()
		}
		return
	} else {
		if reqEvent, exist = c.RenameFrom[eventId]; exist {
			c.cacheLock.Lock()
			delete(c.RenameFrom, reqEvent.ID)
			c.cacheLock.Unlock()
		}
		return
	}
}

/*
Check for expired events
3 types of event are in the cache
1- the ones that have been matched
2- the unmatched ones with expired timer
3- the unmatched ones waiting for its match
This funtion keep removing the first event in the list untill it reaches the 3rd case
*/
func (c *Cache) FindExpiredEvents() {
	itemRemoved := true
	for c.timers.NumberOfItems > 0 && itemRemoved {
		if c.timeDifference(c.timers.Head().T)*10000 > int(1*time.Millisecond) {
			//The item is expired
			if c.EventExists(c.timers.Head().ID) {
				event, matchedEvent, eventExist, matchExist, eventType := c.CheckForMatch(c.timers.Head().ID)
				if eventType == "RENAME_TO" {
					c.createRenameEvent(matchedEvent, event, matchExist, eventExist)
				} else {
					c.createRenameEvent(event, matchedEvent, eventExist, matchExist)
				}
			}
			c.cacheLock.Lock()
			c.timers.moveHead()
			c.cacheLock.Unlock()
		} else if !c.EventExists(c.timers.Head().ID) {
			//check the first case
			c.cacheLock.Lock()
			c.timers.moveHead()
			c.cacheLock.Unlock()
		} else {
			//third case happened
			itemRemoved = false //break the loop if the head has not changed
		}
	}
}

func (c *Cache) BroadcastRenameEvent(e Event) {
	events := make([]Event, 1)
	events[0] = e
	c.ProcessedEvents <- events
	//c.FindExpiredEvents()
}

func (c *Cache) Start(es *EventStream) {
	c.RenameFrom = make(map[uint64]Event)
	c.RenameTo = make(map[uint64]Event)

	c.timers = TimerCache{make([]TimerCacheItem, MaxCapacity), 0, MaxCapacity, 0, 0}
	c.ProcessedEvents = es.Events

	/*
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
		}()*/
}
