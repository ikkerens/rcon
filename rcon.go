package rcon

import (
	"errors"
	"sync"
	"time"

	"github.com/james4k/rcon"
)

type Conn struct {
	host, pass string

	pending  map[int]chan string
	lock     sync.Mutex
	sendLock sync.Mutex

	backing *rcon.RemoteConsole
	closed  bool
}

func New(host, pass string) (*Conn, error) {
	r := &Conn{
		host:    host,
		pass:    pass,
		pending: make(map[int]chan string),
	}

	if err := r.connect(); err != nil {
		return nil, err
	}
	go r.reader()

	return r, nil
}

func (conn *Conn) connect() error {
	nc, err := rcon.Dial(conn.host, conn.pass)
	if err != nil {
		return err
	}

	conn.lock.Lock()
	defer conn.lock.Unlock()

	conn.backing = nc

	return nil
}

func (conn *Conn) Close() error {
	conn.lock.Lock()
	defer conn.lock.Unlock()

	conn.closed = true
	if conn.backing != nil {
		oc := conn.backing
		conn.backing = nil
		return oc.Close()
	}
	return nil
}

func (conn *Conn) Send(cmd string) (response string, err error) {
	conn.lock.Lock()
	c := conn.backing
	if c == nil {
		if conn.closed {
			conn.lock.Unlock()
			return "", errors.New("no active rcon connection (closed)")
		}
		conn.lock.Unlock()

		return "", errors.New("no active rcon connection (reconnecting)")
	}
	conn.lock.Unlock()

	conn.sendLock.Lock()
	defer conn.sendLock.Unlock()
	msgId, err := c.Write(cmd)
	if err != nil {
		return "", err
	}

	ch := make(chan string, 1)
	conn.pending[msgId] = ch

	t := time.After(5 * time.Second)
	select {
	case msg := <-ch:
		return msg, nil
	case <-t:
		conn.invalidate()
		return "", errors.New("timeout on receiving rcon message")
	}
}

func (conn *Conn) reader() {
	for {
		msg, msgId, err := conn.backing.Read()
		if err != nil {
			conn.invalidate()
			return
		}

		conn.lock.Lock()
		if ch, exists := conn.pending[msgId]; exists {
			ch <- msg
			delete(conn.pending, msgId)
		}
		conn.lock.Unlock()
	}
}

func (conn *Conn) invalidate() {
	conn.lock.Lock()
	defer conn.lock.Unlock()

	if !conn.closed {
		conn.pending = make(map[int]chan string)
		if conn.backing != nil {
			_ = conn.backing.Close()
			conn.backing = nil
		}
		go conn.reconnect()
	}
}

func (conn *Conn) reconnect() {
	for {
		conn.lock.Lock()
		if conn.closed {
			return
		}
		conn.lock.Unlock()

		if err := conn.connect(); err == nil {
			return // connection has returned, break out
		}

		time.Sleep(time.Second) // try again in a second
	}
}
