//
// Copyright © 2011-2017 Guy M. Allard
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package stompngo

import (
	"bufio"
	"bytes"
	"net"
	// "bytes"
	"strconv"
	"time"
)

/*
	Logical network writer.  Read wiredata structures from the communication
	channel, and put the frame on the wire.
*/
func (c *Connection) writer() {
writerLoop:
	for {
		select {
		case d := <-c.output:
			c.log("WTR_WIREWRITE start")
			c.wireWrite(d)
			c.log("WTR_WIREWRITE COMPLETE", d.frame.Command, d.frame.Headers,
				HexData(d.frame.Body))
			if d.frame.Command == DISCONNECT {
				break writerLoop // we are done with this connection
			}
		case _ = <-c.ssdc:
			c.log("WTR_WIREWRITE shutdown S received")
			break writerLoop
		case _ = <-c.wtrsdc:
			c.log("WTR_WIREWRITE shutdown W received")
			break writerLoop
		}
	} // of for
	//
	c.connected = false
	c.log("WTR_SHUTDOWN", time.Now())
}

/*
	Connection logical write.
*/
func (c *Connection) wireWrite(d wiredata) {
	f := &d.frame
	// fmt.Printf("WWD01 f:[%v]\n", f)
	switch f.Command {
	case "\n": // HeartBeat frame
		if c.dld.wde && c.dld.dns {
			_ = c.netconn.SetWriteDeadline(time.Now().Add(c.dld.wdld))
		}
		_, e := c.wtr.WriteString(f.Command)
		if e != nil {
			if e.(net.Error).Timeout() {
				if c.dld.dns {
					c.dld.dlnotify(e, true)
				}
			}
			d.errchan <- e
			return
		}
	default: // Other frames
		if e := f.writeFrame(c.wtr, c); e != nil {
			d.errchan <- e
			return
		}
		if e := c.wtr.Flush(); e != nil {
			d.errchan <- e
			return
		}
	}
	if e := c.wtr.Flush(); e != nil {
		d.errchan <- e
		return
	}
	//
	if c.hbd != nil {
		c.hbd.sdl.Lock()
		c.hbd.ls = time.Now().UnixNano() // Latest good send
		c.hbd.sdl.Unlock()
	}
	c.mets.tfw++                // Frame written count
	c.mets.tbw += f.Size(false) // Bytes written count
	//
	d.errchan <- nil
	return
}

/*
	Physical frame write to the wire.
*/
func (f *Frame) writeFrame(w *bufio.Writer, c *Connection) error {

	var sctok bool
	// Content type.  Always add it if the client does not suppress and does not
	// supply it.
	_, sctok = f.Headers.Contains(HK_SUPPRESS_CT)
	if !sctok {
		if _, ctok := f.Headers.Contains(HK_CONTENT_TYPE); !ctok {
			f.Headers = append(f.Headers, HK_CONTENT_TYPE,
				DFLT_CONTENT_TYPE)
		}
	}

	var sclok bool
	// Content length - Always add it if client does not suppress it and
	// does not supply it.
	_, sclok = f.Headers.Contains(HK_SUPPRESS_CL)
	if !sclok {
		if _, clok := f.Headers.Contains(HK_CONTENT_LENGTH); !clok {
			f.Headers = append(f.Headers, HK_CONTENT_LENGTH, strconv.Itoa(len(f.Body)))
		}
	}
	// Encode the headers if needed
	if c.Protocol() > SPL_10 && f.Command != CONNECT {
		for i := 0; i < len(f.Headers); i += 2 {
			f.Headers[i] = encode(f.Headers[i])
			f.Headers[i+1] = encode(f.Headers[i+1])
		}
	}

	if sclok {
		nz := bytes.IndexByte(f.Body, 0)
		// fmt.Printf("WDBG41 ok:%v\n", nz)
		if nz == 0 {
			f.Body = []byte{}
			// fmt.Printf("WDBG42 body:%v bodystring: %v\n", f.Body, string(f.Body))
		} else if nz > 0 {
			f.Body = f.Body[0:nz]
			// fmt.Printf("WDBG43 body:%v bodystring: %v\n", f.Body, string(f.Body))
		}
	}

	if c.dld.wde && c.dld.dns {
		_ = c.netconn.SetWriteDeadline(time.Now().Add(c.dld.wdld))
	}

	// Writes start

	// Write the frame Command
	_, e := w.WriteString(f.Command + "\n")
	if c.checkWriteError(e) != nil {
		return e
	}
	// fmt.Println("WRCMD", f.Command)
	// Write the frame Headers
	for i := 0; i < len(f.Headers); i += 2 {
		if c.dld.wde && c.dld.dns {
			_ = c.netconn.SetWriteDeadline(time.Now().Add(c.dld.wdld))
		}
		_, e := w.WriteString(f.Headers[i] + ":" + f.Headers[i+1] + "\n")
		if c.checkWriteError(e) != nil {
			return e
		}
		// fmt.Println("WRHDR", f.Headers[i]+":"+f.Headers[i+1]+"\n")
	}

	// Write the last Header LF
	if c.dld.wde && c.dld.dns {
		_ = c.netconn.SetWriteDeadline(time.Now().Add(c.dld.wdld))
	}
	e = w.WriteByte('\n')
	if c.checkWriteError(e) != nil {
		return e
	}
	// fmt.Printf("WDBG40 ok:%v\n", sclok)

	// Write the body
	if len(f.Body) != 0 { // Foolish to write 0 length data
		// fmt.Println("WRBDY", f.Body)
		e := c.writeBody(f)
		if c.checkWriteError(e) != nil {
			return e
		}
	}
	if c.dld.wde && c.dld.dns {
		_ = c.netconn.SetWriteDeadline(time.Now().Add(c.dld.wdld))
	}
	e = w.WriteByte(0)
	if c.checkWriteError(e) != nil {
		return e
	}
	// End of write loop - set no deadline
	if c.dld.wde {
		_ = c.netconn.SetWriteDeadline(c.dld.t0)
	}
	return nil
}

func (c *Connection) checkWriteError(e error) error {
	if e == nil {
		return e
	}
	ne, ok := e.(net.Error)
	if !ok {
		return e
	}
	if ne.Timeout() {
		if c.dld.dns {
			c.log("invoking write deadline callback 1")
			c.dld.dlnotify(e, true)
		}
	}
	return e
}

func (c *Connection) writeBody(f *Frame) error {
	// fmt.Printf("WDBG99 body:%v bodystring: %v\n", f.Body, string(f.Body))
	var n = 0
	var e error
	for {
		if c.dld.wde && c.dld.dns {
			_ = c.netconn.SetWriteDeadline(time.Now().Add(c.dld.wdld))
		}
		n, e = c.wtr.Write(f.Body)
		if n == len(f.Body) {
			return e
		}
		c.log("SHORT WRITE", n, len(f.Body))
		if n == 0 { // Zero bytes would mean something is seriously wrong.
			return e
		}
		if !c.dld.rfsw {
			return e
		}
		if c.dld.wde && c.dld.dns && isErrorTimeout(e) {
			c.log("invoking write deadline callback 2")
			c.dld.dlnotify(e, true)
		}
		// *Any* error from a bufio.Writer is *not* recoverable.  See code in
		// bufio.go to understand this.  We get a new writer here, to clear any
		// error condition.
		c.wtr = bufio.NewWriter(c.netconn) // Create new writer
		f.Body = f.Body[n:]
	}
}

func isErrorTimeout(e error) bool {
	if e == nil {
		return false
	}
	_, ok := e.(net.Error)
	if !ok {
		return false
	}
	return true
}
