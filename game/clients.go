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

var (
	worldW = 30
	worldH = 10
	worldD = 30
)
var level [][][]int64

func init() {
	gen()
}

func gen() {
	level = make([][][]int64, worldW)
	for x := 0; x < worldW; x += 1 {
		level[x] = make([][]int64, worldH)
		for y := 0; y < worldH; y += 1 {
			level[x][y] = make([]int64, worldD)
		}
	}

	slope := 10
	for x := 0; x < worldW; x += 1 {
		slope += 1 - rand.Intn(3)
		if slope < 4 {
			slope = 4
		}
		if slope > 8 {
			slope = 8
		}

		length := worldD - slope
		for y := 0; y < worldH; y += 1 {
			if length < (worldH-y)/2 {
				length = (worldH - y) / 2
			}
			for z := 1; z <= length; z += 1 {
				level[x][y][z] = 2
			}
			length -= 4

		}
	}
	for x := 0; x < worldW; x += 1 {
		for z := 1; z < worldD; z += 1 {
			level[x][0][z] = 1
		}
	}

}

type Entity struct {
	x        float64
	y        float64
	z        float64
	xspeed   float64
	yspeed   float64
	zspeed   float64
	id       uint32
	dirty    bool
	owner    *Client
	grounded bool
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
	e.z = 20
	e.y = 10
	e.x = 20
	for {
		time.Sleep(time.Millisecond * 100)
		nearest := e.nearestEntity()
		if nearest == nil {
			continue
		}

		dx := e.x - nearest.x
		dz := e.z - nearest.z
		dist := math.Sqrt(dx*dx + dz*dz)

		if dist > 1.5 {
			e.xspeed = (dx / dist) * -.3
			e.zspeed = (dz / dist) * -.3
		} else if dist < .1 {
			e.xspeed = (rand.Float64() - .5) * .4
			e.zspeed = (rand.Float64() - .5) * .4
		} else if dist < 1.0 {
			e.xspeed = (dx / dist) * .3
			e.zspeed = (dz / dist) * .3
		} else {
			e.xspeed = 0
			e.zspeed = 0
		}

		if e.yspeed == 0 {
			e.y -= 1
			grounded := e.Colliding()
			e.y += 1
			if grounded {
				e.x += e.xspeed
				e.z += e.zspeed
				nearWall := e.Colliding()
				e.x -= e.xspeed
				e.z -= e.zspeed
				if nearWall {
					e.yspeed = .32
				}
			}
		}

		log.Printf("X%f Z%f\n", e.xspeed, e.zspeed)
		e.dirty = true
	}
}

func (e *Entity) nearestEntity() (nearest *Entity) {
	entityLock.RLock()
	defer entityLock.RUnlock()

	dist := math.Inf(1)
	for _, ent := range entity {
		if ent != e && ent != nil {
			newDist := math.Pow(e.x-ent.x, 2) + math.Pow(e.z-ent.z, 2)
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
		e.zspeed = (rand.Float64() - .5) * 4
		e.dirty = true
	}
}

var tickTime = time.Millisecond * 40
var serverTicks = time.NewTicker(tickTime)
var updates = make(chan func())

func ProcessWorld() {
	for range [3]struct{}{} {
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
					c.Send("MOVE", id, ent.x, ent.y, ent.z, ent.xspeed, ent.yspeed, ent.zspeed)
				}
			}
		} else {
			if ent != nil {
				c.Send("NEW", id, "PLAYER")
				c.Send("MOVE", id, ent.x, ent.y, ent.z, ent.xspeed, ent.yspeed, ent.zspeed)
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
	ent.x = 20
	ent.y = 20
	ent.z = 20
	ent.owner = c
	c.owns = ent

	clientLock.Lock()
	client[c] = c
	clientLock.Unlock()

	c.Send("WORLD", level)
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
					ent.z = msg[4].(float64)
					ent.xspeed = msg[5].(float64)
					ent.yspeed = msg[6].(float64)
					ent.zspeed = msg[7].(float64)
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
	playerSize := .5
	gravity := -.05
	e.x += e.xspeed
	if e.Colliding() {
		e.y += .3
		if e.Colliding() {
			e.y -= .3
			e.x -= e.xspeed
		}
	}
	e.z += e.zspeed
	if e.Colliding() {
		e.y += .3
		if e.Colliding() {
			e.y -= .3
			e.z -= e.zspeed
		}
	}
	e.yspeed += gravity
	e.y += e.yspeed
	if e.Colliding() {
		e.y -= e.yspeed
		if e.yspeed < 0 {
			e.grounded = true
		}
		e.yspeed = 0
	} else {
		e.grounded = false
	}

	if e.x < playerSize/2 {
		e.x = playerSize / 2
		e.xspeed = 0
	}
	if e.x > float64(worldW)+1-playerSize/2 {
		e.x = float64(worldW) + 1 - playerSize/2
		e.xspeed = 0
	}
	if e.z < playerSize/2 {
		e.z = playerSize / 2
		e.zspeed = 0
	}
	if e.z > float64(worldD)+1-playerSize/2 {
		e.z = float64(worldD) + 1 - playerSize/2

	}
}

func (e *Entity) Colliding() bool {
	playerSize := .5
	sx := int(math.Floor(e.x - playerSize/2))
	sy := int(math.Floor(e.y - playerSize/2))
	sz := int(math.Floor(e.z - playerSize/2))
	lx := int(math.Ceil(e.x + playerSize/2))
	ly := int(math.Ceil(e.y + playerSize/2))
	lz := int(math.Ceil(e.z + playerSize/2))
	if sx < 0 || lx >= worldW || sz < 0 || lz >= worldD {
		return true
	}
	if sy < 0 || ly >= worldH {
		return false
	}
	for ix := sx; ix < lx; ix += 1 {
		for iy := sy; iy < ly; iy += 1 {
			for iz := sz; iz < lz; iz += 1 {
				blockType := level[ix][iy][iz]
				if blockType > 0 {
					return true
				}
			}
		}
	}
	return false
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
