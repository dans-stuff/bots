package game

import "log"
import "golang.org/x/net/websocket"
import "encoding/json"
import "sync"
import "time"
import "math/rand"
import "math"

const maxEntityId = 255

var defaultLatency = int64(30)

var entity = map[uint32]*Entity{}
var entityLock sync.RWMutex
var nextId = uint32(0)

var client = map[*Client]*Client{}
var clientLock sync.RWMutex

type Entity struct {
	x      float64
	y      float64
	xspeed float64
	yspeed float64
	id     uint32
	dirty  bool
	owner  *Client
}

type Client struct {
	latency   float64
	lastPing  time.Time
	ponged    bool
	pinger    *time.Ticker
	c         *websocket.Conn
	encoder   *json.Encoder
	origin    string
	owns      *Entity
	knownEnts map[uint32]struct{}
}

func (e *Entity) AIChase() {
	for {
		time.Sleep(time.Millisecond * 100)
		nearest := e.nearestEntity()
		if nearest == nil {
			continue
		}

		dx := e.x - nearest.x
		dy := e.y - nearest.y
		dist := math.Sqrt(dx*dx + dy*dy)
		log.Printf("X%f Y%f D%f\n", dx, dy, dist)

		if dist > 15 {
			e.xspeed = (dx / dist) * -3
			e.yspeed = (dy / dist) * -3
		} else if dist < 1 {
			e.xspeed = (rand.Float64() - .5) * 4
			e.yspeed = (rand.Float64() - .5) * 4
		} else if dist < 10 {
			e.xspeed = (dx / dist) * 3
			e.yspeed = (dy / dist) * 3
		} else {
			e.xspeed = 0
			e.yspeed = 0
		}

		log.Printf("X%f Y%f\n", e.xspeed, e.yspeed)
		e.dirty = true
	}
}

func (e *Entity) nearestEntity() (nearest *Entity) {
	entityLock.RLock()
	defer entityLock.RUnlock()

	dist := math.Inf(1)
	for _, ent := range entity {
		if ent != e && ent != nil {
			newDist := math.Pow(e.x-ent.x, 2) + math.Pow(e.y-ent.y, 2)
			if newDist < dist {
				nearest = ent
				dist = newDist
			}
		}
	}
	return nearest
}

func (e *Entity) AIRandom() {
	for {
		time.Sleep(time.Second)
		e.xspeed = (rand.Float64() - .5) * 4
		e.yspeed = (rand.Float64() - .5) * 4
		e.dirty = true
	}
}

var tickTime = time.Millisecond * 40
var serverTicks = time.NewTicker(tickTime)
var updates = make(chan func())

func ProcessWorld() {
	for range [10]struct{}{} {
		ent := NewEntity()
		defer ent.Delete()
		go ent.AIChase()
	}

	for range serverTicks.C {
		// log.Println("Starting world tick")
		applyInputs()
		sendUpdates()
		deleteOldEnts()
		simulatePhysics()
		// log.Println("Finishing world tick")
	}
}

func applyInputs() {
	num := 0
	for {
		select {
		case act := <-updates:
			num += 1
			act()
		default:
			if num > 0 {
				log.Println("Applied", num, "updates")
			}
			return
		}
	}
}

func sendUpdates() {
	entityLock.RLock()
	defer entityLock.RUnlock()
	clientLock.RLock()
	defer clientLock.RUnlock()

	for cli, _ := range client {
		cli.syncWorld()
	}
}

func simulatePhysics() {
	entityLock.RLock()
	defer entityLock.RUnlock()

	for _, ent := range entity {
		if ent != nil {
			ent.Simulate()
		}
	}
}

func init() {
	go ProcessWorld()
}

func (c *Client) syncWorld() {
	for id, ent := range entity {
		_, found := c.knownEnts[id]
		if found {
			if ent == nil {
				delete(c.knownEnts, id)
				c.Send("DEL", id)
			} else {
				if ent.dirty {
					c.Send("MOVE", id, ent.x, ent.y, ent.xspeed, ent.yspeed)
				}
			}
		} else {
			if ent != nil {
				c.Send("NEW", id, "PLAYER")
				c.Send("MOVE", id, ent.x, ent.y, ent.xspeed, ent.yspeed)
				c.knownEnts[id] = struct{}{}
				if ent.owner == c {
					c.Send("CONTROL", id)
				}
			}
		}
	}
}

func (c *Client) Connect(s *websocket.Conn) {
	c.knownEnts = make(map[uint32]struct{})

	c.c = s
	c.pinger = time.NewTicker(time.Second)
	c.latency = 5
	c.ponged = true
	encoder := json.NewEncoder(s)
	c.encoder = encoder
	c.origin = s.Request().RemoteAddr
	log.Println("+", c.origin)

	ent := NewEntity()
	ent.owner = c
	c.owns = ent

	clientLock.Lock()
	client[c] = c
	clientLock.Unlock()

	go c.Pings()
	c.ReadForever()
}

func (c *Client) Pings() {
	for newTime := range c.pinger.C {
		if c.ponged {
			c.lastPing = newTime
			c.Send("PING")
		}
	}
}

func (c *Client) Send(data ...interface{}) {
	if data[0].(string) != "PING" {
		log.Println(">", c.origin, data)
	}
	c.encoder.Encode(data)
}

func (c *Client) QueueAction(act func()) {
	offset := defaultLatency - int64(c.latency)
	time.AfterFunc(time.Millisecond*time.Duration(offset), func() {
		updates <- act
	})
}

func (c *Client) ReadForever() {
	dec := json.NewDecoder(c.c)

	var err error
	for {
		msg := []interface{}{}
		err = dec.Decode(&msg)
		if err != nil {
			break
		}
		msgCommand := msg[0].(string)

		if msgCommand != "PONG" {
			log.Println("<", c.origin, msg)
		}

		switch msgCommand {
		case "PONG":
			diff := time.Now().Sub(c.lastPing)
			c.latency = (c.latency + diff.Seconds()*500*2) / 3
			// log.Println("Latency", diff.Seconds(), c.origin, c.latency, "ms")
			c.ponged = true
		case "SAY":
		case "MOVE":
			msg := msg
			act := func() {
				ent := GetEntity(uint32(msg[1].(float64)))
				if ent.owner == c {
					ent.dirty = true
					ent.x = msg[2].(float64)
					ent.y = msg[3].(float64)
					ent.xspeed = msg[4].(float64)
					ent.yspeed = msg[5].(float64)
				} else {
					log.Println("HACK ATTEMPT", c.origin)
				}
			}
			c.QueueAction(act)
		}

	}
	c.Disconnect(err)
}

func (c *Client) Disconnect(err error) {
	c.pinger.Stop()
	log.Println("-", c.origin, err)

	clientLock.Lock()
	delete(client, c)
	clientLock.Unlock()

	if c.owns != nil {
		act := func() {
			c.owns.Delete()
		}
		c.QueueAction(act)
	}
}

func NewEntity() *Entity {
	entityLock.Lock()
	defer entityLock.Unlock()
	for {
		if _, found := entity[nextId]; !found {
			break
		}
		if nextId >= maxEntityId {
			nextId = 0
		} else {
			nextId += 1
		}
	}

	id := nextId
	nextId += 1

	newEnt := &Entity{id: id}
	entity[id] = newEnt
	log.Printf("Creating new entity: %d\n", id)
	return newEnt
}

func (e *Entity) Delete() {
	entityLock.Lock()
	defer entityLock.Unlock()
	log.Printf("Deleting entity: %d\n", e.id)
	entity[e.id] = nil
	log.Printf("Entities: %#v\n", entity)
}

func (e *Entity) Simulate() {
	e.x += e.xspeed
	e.y += e.yspeed
	if e.x > 297 {
		e.x = 297
	}
	if e.y > 197 {
		e.y = 197
	}
	if e.x < 3 {
		e.x = 3
	}
	if e.y < 3 {
		e.y = 3
	}
}

func deleteOldEnts() {
	entityLock.Lock()
	defer entityLock.Unlock()
	dels := []uint32{}
	for id, ent := range entity {
		if ent == nil {
			dels = append(dels, id)
		} else {
			ent.dirty = false
		}
	}
	for _, id := range dels {
		delete(entity, id)
	}
}

func GetEntity(id uint32) *Entity {
	entityLock.RLock()
	defer entityLock.RUnlock()
	if e, found := entity[id]; found {
		return e
	}
	return nil
}

func ClientConnected(s *websocket.Conn) {
	(&Client{}).Connect(s)
}
