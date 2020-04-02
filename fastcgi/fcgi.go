package fastcgi

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"sync"
)

type header struct {
	Version       uint8
	Type          recType
	ID            uint16
	ContentLength uint16
	PaddingLength uint8
	Reserved      uint8
}

//for padding so we don't have to allocate all the time
//not synchronized because we don't care what the contents are
var pad [maxPad]byte

func (h *header) init(recType recType, reqID uint16, contentLength int) {
	h.Version = 1
	h.Type = recType
	h.ID = reqID
	h.ContentLength = uint16(contentLength)
	h.PaddingLength = uint8(-contentLength & 7)
}

type record struct {
	h   header
	buf [maxWrite + maxPad]byte
}

func (rec *record) read(r io.Reader) (err error) {
	if err = binary.Read(r, binary.BigEndian, &rec.h); err != nil {
		return err
	}

	if rec.h.Version != 1 {
		return errors.New("fastcgi: invalid header version")
	}

	n := int(rec.h.ContentLength) + int(rec.h.PaddingLength)
	if _, err = io.ReadFull(r, rec.buf[:n]); err != nil {
		return err
	}

	return nil
}

func (rec *record) content() []byte {
	return rec.buf[:rec.h.ContentLength]
}

//conn sends records over rwc
type conn struct {
	mutex sync.Mutex
	rwc   io.ReadWriteCloser

	//to avoid allocations
	buf bytes.Buffer
	h   header
}

func newConn(rwc io.ReadWriteCloser) *conn {
	return &conn{
		rwc: rwc,
	}
}

func (c *conn) Close() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	return c.rwc.Close()
}

//writeRecord writes and sends a single record.
func (c *conn) writeRecord(recType recType, reqID uint16, b []byte) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.buf.Reset()

	c.h.init(recType, reqID, len(b))

	if err := binary.Write(&c.buf, binary.BigEndian, c.h); err != nil {
		return err
	}

	if _, err := c.buf.Write(b); err != nil {
		return err
	}

	if _, err := c.buf.Write(pad[:c.h.PaddingLength]); err != nil {
		return err
	}

	_, err := c.rwc.Write(c.buf.Bytes())

	return err
}

func (c *conn) writeBeginRequest(reqID uint16, role uint16, flags uint8) error {
	b := [8]byte{
		byte(role >> 8),
		byte(role),
		flags & 1,
	}

	return c.writeRecord(typeBeginRequest, reqID, b[:])
}

func (c *conn) writeEndRequest(reqID uint16, appStatus int, protocolStatus uint8) error {
	b := make([]byte, 8)
	binary.BigEndian.PutUint32(b, uint32(appStatus))
	b[4] = protocolStatus

	return c.writeRecord(typeEndRequest, reqID, b)
}

func (c *conn) writeAbortRequest(reqID uint16) error {
	return c.writeRecord(typeAbortRequest, reqID, nil)
}

func (c *conn) writePairs(recType recType, reqID uint16, pairs map[string]string) error {
	w := newWriter(c, recType, reqID)
	b := make([]byte, 8)

	for k, v := range pairs {
		n := encodeSize(b, uint32(len(k)))
		n += encodeSize(b[n:], uint32(len(v)))

		if _, err := w.Write(b[:n]); err != nil {
			return err
		}

		if _, err := w.WriteString(k); err != nil {
			return err
		}

		if _, err := w.WriteString(v); err != nil {
			return err
		}
	}

	if err := w.Close(); err != nil {
		return err
	}

	return nil
}

func readSize(s []byte) (uint32, int) {
	if len(s) == 0 {
		return 0, 0
	}

	size, n := uint32(s[0]), 1

	if size & (1 << 7) != 0 {
		if len(s) < 4 {
			return 0, 0
		}

		n = 4
		size = binary.BigEndian.Uint32(s)
		size &^= 1 << 31
	}

	return size, n
}

func readString(s []byte, size uint32) string {
	if size > uint32(len(s)) {
		return ""
	}

	return string(s[:size])
}

func encodeSize(b []byte, size uint32) int {
	if size > 127 {
		size |= 1 << 31
		binary.BigEndian.PutUint32(b, size)

		return 4
	}

	b[0] = byte(size)

	return 1
}

//bufWriter encapsulates bufio.Writer but also closes the underlying stream when
type bufWriter struct {
	closer io.Closer
	*bufio.Writer
}

func (w *bufWriter) Close() error {
	if err := w.Writer.Flush(); err != nil {
		_ = w.closer.Close()

		return err
	}

	return w.closer.Close()
}

//streamWriter abstracts out the separation of a stream into discrete records.
//It only writes maxWrite bytes at a time.
type streamWriter struct {
	c       *conn
	recType recType
	reqID   uint16
}

func newWriter(c *conn, recType recType, reqID uint16) *bufWriter {
	s := &streamWriter{
		c: c,
		recType: recType,
		reqID: reqID,
	}

	w := bufio.NewWriterSize(s, maxWrite)

	return &bufWriter{s, w}
}

func (w *streamWriter) Write(p []byte) (int, error) {
	nn := 0

	for len(p) > 0 {
		n := len(p)
		if n > maxWrite {
			n = maxWrite
		}

		if err := w.c.writeRecord(w.recType, w.reqID, p[:n]); err != nil {
			return nn, err
		}

		nn += n
		p = p[n:]
	}

	return nn, nil
}

//send empty record to close the stream
func (w *streamWriter) Close() error {
	return w.c.writeRecord(w.recType, w.reqID, nil)
}
