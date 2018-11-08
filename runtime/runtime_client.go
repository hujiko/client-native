package runtime

import (
	"fmt"
	"log"
	"net"
	"strings"
	"time"
)

//RuntimeTaskResponse ...
type RuntimeTaskResponse struct {
	result string
	err    error
}

//RuntimeTask has command to execute on runtime api, and response channel for result
type RuntimeTask struct {
	command  string
	response chan RuntimeTaskResponse
}

//Client handles multiple HAProxy clients
type Client struct {
	SocketsPath []string
}

//SingleRuntime handles one runtime API
type SingleRuntime struct {
	socketOpen       bool
	jobs             chan RuntimeTask
	socketPath       string
	autoReconnect    bool
	runtimeAPIsocket net.Conn
}

//Init must be given path to runtime socket
func (s *SingleRuntime) Init(socketPath string, autoReconnect bool) error {
	s.socketPath = socketPath
	s.autoReconnect = autoReconnect
	s.jobs = make(chan RuntimeTask)
	s.socketConnect()
	go s.handleIncommingJobs()
	return nil
}

func (s *SingleRuntime) socketConnect() error {
	var err error
	s.runtimeAPIsocket, err = net.Dial("unix", s.socketPath)
	if err != nil {
		if s.autoReconnect {
			go func() {
				time.Sleep(time.Second * 1)
				s.socketConnect()
			}()
		}
		return err
	}
	s.socketOpen = true
	_, err = s.runtimeAPIsocket.Write([]byte(fmt.Sprintf("prompt\n")))
	if err != nil {
		return err
	}
	return nil
}

func (s *SingleRuntime) handleIncommingJobs() {
	log.Println("start")
	for {
		select {
		case job := <-s.jobs:
			result, err := s.readFromSocket(s.runtimeAPIsocket, job.command)
			if err != nil {
				job.response <- RuntimeTaskResponse{err: err}
			} else {
				job.response <- RuntimeTaskResponse{result: result}
			}
		case <-time.After(time.Duration(60) * time.Second):
			log.Println(s.readFromSocket(s.runtimeAPIsocket, "show env"))
		}
	}
	defer s.runtimeAPIsocket.Close()
}

func (s *SingleRuntime) readFromSocket(c net.Conn, command string) (string, error) {
	if !s.socketOpen {
		return "", fmt.Errorf("no connection")
	}
	_, err := c.Write([]byte(fmt.Sprintf("%s\n", command)))
	if err != nil {
		s.socketOpen = false
		c.Close()
		return "", err
	}
	time.Sleep(1e9)
	bufferSize := 1024
	buf := make([]byte, bufferSize)
	var data strings.Builder
	for {
		n, err := c.Read(buf[:])
		if err != nil {
			break
		}
		data.Write(buf[0:n])
		if n < bufferSize {
			break
		}
	}
	return strings.TrimSuffix(data.String(), "\n\n> "), nil
}

func (s *SingleRuntime) readFromSocketClean(command string) (string, error) {
	c, err := net.Dial("unix", s.socketPath)
	if err != nil {
		return "", err
	}
	defer c.Close()

	_, err = c.Write([]byte(fmt.Sprintf("%s\n", command)))
	if err != nil {
		return "", nil
	}
	time.Sleep(1e9)
	buf := make([]byte, 1024)
	var data strings.Builder
	for {
		n, err := c.Read(buf[:])
		if err != nil {
			break
		}
		data.Write(buf[0:n])
	}
	return data.String(), nil
}

//ExecuteRaw executes command on runtime API and returns raw result
func (s *SingleRuntime) ExecuteRaw(command string) (string, error) {
	//allow one retry if connection breaks temporarily
	return s.executeRaw(command, 1)
}

func (s *SingleRuntime) executeRaw(command string, retry int) (string, error) {
	response := make(chan RuntimeTaskResponse)
	RuntimeTask := RuntimeTask{
		command:  command,
		response: response,
	}
	s.jobs <- RuntimeTask
	select {
	case rsp := <-response:
		if rsp.err != nil && retry > 0 {
			if !s.socketOpen || s.runtimeAPIsocket == nil {
				s.socketConnect()
			}
			retry--
			return s.executeRaw(command, retry)
		}
		return rsp.result, rsp.err
	case <-time.After(time.Duration(30) * time.Second):
		return "", fmt.Errorf("timeout reached")
	}
}