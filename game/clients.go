package game

import "log"
import "golang.org/x/net/websocket"
import "encoding/json"
import "sync"
import "time"

const maxEntities = 10e6

var entity [maxEntities]interface{}
var clientIds []uint32
var nextId = uint32(0)
var clientConnLock sync.RWMutex

type Client struct {
	latency  float64
	lastPing time.Time
	ponged   bool
	pinger   *time.Ticker
	id       uint32
	c        *websocket.Conn
	encoder  *json.Encoder
	name     string
	x        float64
	y        float64
	xspeed   float64
	yspeed   float64
}

func (c *Client) Connect(s *websocket.Conn) {
	c.c = s
	c.pinger = time.NewTicker(time.Second)
	c.latency = 5
	c.ponged = true
	encoder := json.NewEncoder(s)
	c.encoder = encoder
	ip := s.Request().RemoteAddr
	log.Println("+", c.id, ip)

	clientConnLock.RLock()
	for _, otherId := range clientIds {
		entity := entity[otherId].(*Client)
		c.Send("NEW", entity.id, "PLAYER")
		c.Send("DUMP", entity.id, entity.name, entity.x, entity.y, entity.xspeed, entity.yspeed)
	}
	clientConnLock.RUnlock()

	go c.Pings()

	c.Send("CONTROL", c.id)
	c.SendOthersNoAge("NEW", c.id, "PLAYER")
	c.SendOthersNoAge("DUMP", c.id, c.name, c.x, c.y, c.xspeed, c.yspeed)

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
	log.Println(">", data)
	c.encoder.Encode(data)
}

func (c *Client) SendRaw(data []byte) {
	log.Println("}", string(data))
	c.c.Write(data)
}

func (c *Client) SendOthers(dataObj ...interface{}) {
	// Injects age as param[1]
	var age float64
	dataObj = append([]interface{}{&age}, dataObj...)
	dataObj[0], dataObj[1] = dataObj[1], dataObj[0]
	clientConnLock.RLock()
	for _, otherId := range clientIds {
		if otherId != c.id {

			dataObj[1] = c.latency + entity[otherId].(*Client).latency
			entity[otherId].(*Client).Send(dataObj...)
		}
	}
	clientConnLock.RUnlock()
}

func (c *Client) SendOthersNoAge(dataObj ...interface{}) {
	clientConnLock.RLock()
	for _, otherId := range clientIds {
		if otherId != c.id {
			entity[otherId].(*Client).Send(dataObj...)
		}
	}
	clientConnLock.RUnlock()
}

func (c *Client) ReadForever() {
	dec := json.NewDecoder(c.c)
	msg := []interface{}{}

	var err error
	for {
		err = dec.Decode(&msg)
		if err != nil {
			break
		}
		log.Println("<", c.id, msg)

		msgCommand := msg[0].(string)

		switch msgCommand {
		case "PONG":
			diff := time.Now().Sub(c.lastPing)
			c.latency = (c.latency + diff.Seconds()*500*2) / 3
			log.Println("Latency", diff.Seconds(), c.id, c.latency, "ms")
			c.ponged = true
		case "SAY":
			c.SendOthers("SAY", c.id, msg[1].(string))
		case "MOVE":
			c.x = msg[1].(float64)
			c.y = msg[2].(float64)
			c.xspeed = msg[3].(float64)
			c.yspeed = msg[4].(float64)
			c.SendOthers("MOVE", c.id, c.x, c.y, c.xspeed, c.yspeed)
		}

	}
	c.Disconnect(err)
}

func (c *Client) Disconnect(err error) {
	log.Println("-", c.id, err)
	c.SendOthersNoAge("DEL", c.id)
}

func ClientConnected(s *websocket.Conn) {
	clientConnLock.Lock()
	for {
		if entity[nextId] == nil {
			break
		}
		if nextId >= maxEntities {
			nextId = 0
		} else {
			nextId += 1
		}
	}

	id := nextId
	nextId += 1

	newClient := &Client{}
	entity[id] = newClient
	newClient.id = id
	clientIds = append(clientIds, id)
	log.Println("IDS", clientIds)
	clientConnLock.Unlock()

	defer func() {
		newClient.pinger.Stop()
		clientConnLock.Lock()
		entity[id] = nil
		idPos := -1
		for i, currId := range clientIds {
			if currId == id {
				idPos = i
				break
			}
		}
		if idPos == -1 {
			log.Println("ERR: ID not found in clientIds, should be impossible")
		} else {
			clientIds[idPos], clientIds = clientIds[len(clientIds)-1], clientIds[:len(clientIds)-1]
		}
		clientConnLock.Unlock()
	}()

	newClient.Connect(s)

}
