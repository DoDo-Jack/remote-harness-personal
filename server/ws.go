package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type wsConn struct {
	conn   net.Conn
	r      *bufio.Reader
	wmu    sync.Mutex
	closed bool
	cmu    sync.Mutex

	fragOp  int
	fragBuf []byte
}

var errWSClosed = errors.New("websocket closed")

func upgradeWS(w http.ResponseWriter, r *http.Request) (*wsConn, error) {
	if !strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") ||
		!strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return nil, errors.New("not a websocket handshake")
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" || r.Header.Get("Sec-WebSocket-Version") != "13" {
		return nil, errors.New("bad websocket version")
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("hijack unsupported")
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}
	h := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	accept := base64.StdEncoding.EncodeToString(h[:])
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte(resp)); err != nil {
		conn.Close()
		return nil, err
	}
	_ = conn.SetWriteDeadline(time.Time{})
	return &wsConn{conn: conn, r: brw.Reader}, nil
}

func (c *wsConn) Close() {
	c.cmu.Lock()
	if c.closed {
		c.cmu.Unlock()
		return
	}
	c.closed = true
	c.cmu.Unlock()
	_ = c.conn.Close()
}

func (c *wsConn) isClosed() bool {
	c.cmu.Lock()
	defer c.cmu.Unlock()
	return c.closed
}

func (c *wsConn) WriteMessage(op int, data []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.isClosed() {
		return errWSClosed
	}
	var head []byte
	n := len(data)
	switch {
	case n < 126:
		head = []byte{byte(0x80 | op), byte(n)}
	case n <= 0xffff:
		head = make([]byte, 4)
		head[0] = byte(0x80 | op)
		head[1] = 126
		binary.BigEndian.PutUint16(head[2:], uint16(n))
	default:
		head = make([]byte, 10)
		head[0] = byte(0x80 | op)
		head[1] = 127
		binary.BigEndian.PutUint64(head[2:], uint64(n))
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(20 * time.Second))
	if _, err := c.conn.Write(head); err != nil {
		c.Close()
		return err
	}
	if n > 0 {
		if _, err := c.conn.Write(data); err != nil {
			c.Close()
			return err
		}
	}
	return nil
}

func (c *wsConn) WriteText(data []byte) error { return c.WriteMessage(0x1, data) }

// ReadMessage 返回一条完整消息；控制帧在内部消化。
func (c *wsConn) ReadMessage(max int) (int, []byte, error) {
	for {
		var h [2]byte
		if _, err := io.ReadFull(c.r, h[:]); err != nil {
			c.Close()
			return 0, nil, err
		}
		fin := h[0]&0x80 != 0
		op := int(h[0] & 0x0f)
		masked := h[1]&0x80 != 0
		length := int64(h[1] & 0x7f)
		switch length {
		case 126:
			var ext [2]byte
			if _, err := io.ReadFull(c.r, ext[:]); err != nil {
				c.Close()
				return 0, nil, err
			}
			length = int64(binary.BigEndian.Uint16(ext[:]))
		case 127:
			var ext [8]byte
			if _, err := io.ReadFull(c.r, ext[:]); err != nil {
				c.Close()
				return 0, nil, err
			}
			length = int64(binary.BigEndian.Uint64(ext[:]))
		}
		if length < 0 || length > int64(max) {
			c.Close()
			return 0, nil, errors.New("frame too large")
		}
		var mask [4]byte
		if masked {
			if _, err := io.ReadFull(c.r, mask[:]); err != nil {
				c.Close()
				return 0, nil, err
			}
		}
		payload := make([]byte, length)
		if length > 0 {
			if _, err := io.ReadFull(c.r, payload); err != nil {
				c.Close()
				return 0, nil, err
			}
			if masked {
				for i := range payload {
					payload[i] ^= mask[i%4]
				}
			}
		}

		switch op {
		case 0x9: // ping
			_ = c.WriteMessage(0xA, payload)
			continue
		case 0xA: // pong
			continue
		case 0x8: // close
			_ = c.WriteMessage(0x8, nil)
			c.Close()
			return 0, nil, errWSClosed
		}

		if op == 0x0 { // 续帧
			c.fragBuf = append(c.fragBuf, payload...)
			if fin {
				out := c.fragBuf
				c.fragBuf = nil
				op2 := c.fragOp
				c.fragOp = 0
				return op2, out, nil
			}
			continue
		}
		if !fin {
			c.fragOp = op
			c.fragBuf = append(c.fragBuf[:0], payload...)
			continue
		}
		return op, payload, nil
	}
}

// StartPing 定期发 ping，防止中间代理把长连接判死。
func (c *wsConn) StartPing(interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for range t.C {
			if c.isClosed() {
				return
			}
			if err := c.WriteMessage(0x9, nil); err != nil {
				return
			}
		}
	}()
}
