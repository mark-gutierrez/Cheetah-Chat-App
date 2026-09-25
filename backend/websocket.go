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
)

// wsMagic is the specific UUID required by the WebSocket RFC to generate the accept key
var wsMagic = []byte("258EAFA5-E914-47DA-95CA-C5AB0DC85B11")

// WSConn wraps the hijacked raw TCP connection
type WSConn struct {
	conn net.Conn
	rw   *bufio.ReadWriter
}

// Upgrade takes a standard HTTP request and upgrades it to a raw WebSocket connection
// entirely using the standard library.
func Upgrade(w http.ResponseWriter, r *http.Request) (*WSConn, error) {
	if r.Header.Get("Upgrade") != "websocket" {
		return nil, errors.New("not a websocket request")
	}

	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		return nil, errors.New("missing Sec-WebSocket-Key")
	}

	// Create the Sec-WebSocket-Accept hash
	h := sha1.New()
	h.Write([]byte(key))
	h.Write(wsMagic)
	accept := base64.StdEncoding.EncodeToString(h.Sum(nil))

	// Hijack the underlying TCP connection from the HTTP server
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("webserver doesn't support hijacking")
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}

	// Send the 101 Switching Protocols response directly over TCP
	rw.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	rw.WriteString("Upgrade: websocket\r\n")
	rw.WriteString("Connection: Upgrade\r\n")
	rw.WriteString("Sec-WebSocket-Accept: " + accept + "\r\n\r\n")
	if err := rw.Flush(); err != nil {
		conn.Close()
		return nil, err
	}

	return &WSConn{conn: conn, rw: rw}, nil
}

// ReadMessage reads a single WebSocket frame
func (ws *WSConn) ReadMessage() ([]byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(ws.rw, header); err != nil {
		return nil, err
	}

	// Opcode 8 means Close connection
	opcode := header[0] & 0x0F
	if opcode == 8 {
		return nil, io.EOF
	}

	masked := header[1]&0x80 != 0
	payloadLen := int(header[1] & 0x7F)

	if payloadLen == 126 {
		extLen := make([]byte, 2)
		if _, err := io.ReadFull(ws.rw, extLen); err != nil {
			return nil, err
		}
		payloadLen = int(binary.BigEndian.Uint16(extLen))
	} else if payloadLen == 127 {
		extLen := make([]byte, 8)
		if _, err := io.ReadFull(ws.rw, extLen); err != nil {
			return nil, err
		}
		payloadLen = int(binary.BigEndian.Uint64(extLen))
	}

	var maskKey []byte
	if masked {
		maskKey = make([]byte, 4)
		if _, err := io.ReadFull(ws.rw, maskKey); err != nil {
			return nil, err
		}
	}

	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(ws.rw, payload); err != nil {
		return nil, err
	}

	// Unmask the payload using the client's mask key
	if masked {
		for i := 0; i < payloadLen; i++ {
			payload[i] ^= maskKey[i%4]
		}
	}

	return payload, nil
}

// WriteMessage sends a text frame (opcode 1) to the client
func (ws *WSConn) WriteMessage(data []byte) error {
	length := len(data)
	var header []byte

	// 0x81 = FIN bit set + Text Frame opcode
	if length < 126 {
		header = []byte{0x81, byte(length)}
	} else if length <= 65535 {
		header = []byte{0x81, 126, 0, 0}
		binary.BigEndian.PutUint16(header[2:], uint16(length))
	} else {
		header = []byte{0x81, 127, 0, 0, 0, 0, 0, 0, 0, 0}
		binary.BigEndian.PutUint64(header[2:], uint64(length))
	}

	if _, err := ws.rw.Write(header); err != nil {
		return err
	}
	if _, err := ws.rw.Write(data); err != nil {
		return err
	}
	return ws.rw.Flush()
}

// Close strictly closes the TCP connection
func (ws *WSConn) Close() error {
	return ws.conn.Close()
}
