/*
 * Licensed to the Apache Software Foundation (ASF) under one or more
 * contributor license agreements.  See the NOTICE file distributed with
 * this work for additional information regarding copyright ownership.
 * The ASF licenses this file to You under the Apache License, Version 2.0
 * (the "License"); you may not use this file except in compliance with
 * the License.  You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package main

import (
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"
)

import (
	gxsync "github.com/dubbogo/gost/sync"
)

import (
	getty "github.com/apache/dubbo-getty"
)

var (
	taskPoolMode = flag.Bool("taskPool", false, "task pool mode")
	taskPoolSize = flag.Int("task_pool_size", 2000, "task poll size")
	pprofPort    = flag.Int("pprof_port", 65432, "pprof http port")
)

var taskPool gxsync.GenericTaskPool

const CronPeriod = 20e9

func main() {
	flag.Parse()

	go func() {
		http.ListenAndServe(fmt.Sprintf(":%d", *pprofPort), nil)
	}()

	options := []getty.ServerOption{getty.WithLocalAddress(":8090")}

	if *taskPoolMode {
		taskPool = gxsync.NewTaskPoolSimple(*taskPoolSize)
		options = append(options, getty.WithServerTaskPool(taskPool))
	}

	server := getty.NewTCPServer(options...)

	go server.RunEventLoop(NewHelloServerSession)
	log.Printf("getty server start, listening at: 8090")

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT)
	<-signals
	server.Close()
}

func NewHelloServerSession(session getty.Session) (err error) {
	pkgHandler := &PackageHandler{}
	EventListener := &MessageHandler{}

	tcpConn, ok := session.Conn().(*net.TCPConn)
	if !ok {
		panic(fmt.Sprintf("newSession: %s, session.conn{%#v} is not tcp connection", session.Stat(), session.Conn()))
	}

	if err = tcpConn.SetNoDelay(true); err != nil {
		return err
	}
	if err = tcpConn.SetKeepAlive(true); err != nil {
		return err
	}
	if err = tcpConn.SetKeepAlivePeriod(10 * time.Second); err != nil {
		return err
	}
	if err = tcpConn.SetReadBuffer(262144); err != nil {
		return err
	}
	if err = tcpConn.SetWriteBuffer(524288); err != nil {
		return err
	}

	session.SetName("hello")
	session.SetMaxMsgLen(128 * 1024) // max message package length is 128k
	session.SetReadTimeout(time.Second)
	session.SetWriteTimeout(5 * time.Second)
	session.SetCronPeriod(int(CronPeriod / 1e6))
	session.SetWaitTime(time.Second)

	session.SetPkgHandler(pkgHandler)
	session.SetEventListener(EventListener)
	return nil
}

type MessageHandler struct {
	SessionOnOpen func(session getty.Session)
}

func (h *MessageHandler) OnOpen(session getty.Session) error {
	log.Printf("OnOpen session{%s} open", session.Stat())
	if h.SessionOnOpen != nil {
		h.SessionOnOpen(session)
	}
	return nil
}

func (h *MessageHandler) OnError(session getty.Session, err error) {
	log.Printf("OnError session{%s} got error{%v}, will be closed.", session.Stat(), err)
}

func (h *MessageHandler) OnClose(session getty.Session) {
	log.Printf("OnClose session{%s} is closing......", session.Stat())
}

func (h *MessageHandler) OnMessage(session getty.Session, pkg interface{}) {
	log.Printf("OnMessage....:%v", pkg)
	time.Sleep(time.Second * 10)
	//s, ok := pkg.(string)
	//if !ok {
	//	log.Printf("illegal package{%#v}", pkg)
	//	return
	//}
	//log.Printf("OnMessage: %s", s)
}

func (h *MessageHandler) OnCron(session getty.Session) {
	log.Printf("OnCron....")
}

type PackageHandler struct{}

func (h *PackageHandler) Read(ss getty.Session, data []byte) (interface{}, int, error) {
	dataLen := len(data)
	if dataLen < 4 {
		return nil, 0, nil
	}

	start := 0
	pos := start + 4
	pkgLen := int(binary.LittleEndian.Uint32(data[start:pos]))
	if dataLen < pos+pkgLen {
		return nil, pos + pkgLen, nil
	}
	start = pos

	pos = start + pkgLen
	s := string(data[start:pos])

	return s, pos, nil
}

func (h *PackageHandler) Write(ss getty.Session, p interface{}) ([]byte, error) {
	pkg, ok := p.(string)
	if !ok {
		log.Printf("illegal pkg:%+v", p)
		return nil, errors.New("invalid package")
	}

	pkgLen := int32(len(pkg))
	pkgStreams := make([]byte, 0, 4+len(pkg))

	// pkg len
	start := 0
	pos := start + 4
	binary.LittleEndian.PutUint32(pkgStreams[start:pos], uint32(pkgLen))
	start = pos

	// pkg
	pos = start + int(pkgLen)
	copy(pkgStreams[start:pos], pkg[:])

	return pkgStreams[:pos], nil
}
