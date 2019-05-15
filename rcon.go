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
	valid   bool
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
	conn.valid = true

	return nil
}

func (conn *Conn) Close() error {
	conn.lock.Lock()
	defer conn.lock.Unlock()

	conn.closed = true
	conn.valid = false
	if conn.backing != nil {
		return conn.backing.Close()
	}
	return nil
}

func (conn *Conn) Send(cmd string) (response string, err error) {
	conn.lock.Lock()
	if conn.valid == false {
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
	msgId, err := conn.backing.Write(cmd)
	if err != nil {
		conn.lock.Unlock()
		return "", err
	}

	ch := make(chan string, 1)
	conn.pending[msgId] = ch

	return <-ch, nil
}

func (conn *Conn) reader() {
	for {
		msg, msgId, err := conn.backing.Read()
		if err != nil {
			conn.lock.Lock()
			if !conn.closed {
				conn.valid = false
				if conn.backing != nil {
					_ = conn.backing.Close()
				}
				conn.backing = nil
				go conn.reconnect()
			}
			conn.lock.Unlock()
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