// Copyright (c) 2020-2021 Doc.ai and/or its affiliates.
//
// SPDX-License-Identifier: Apache-2.0
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at:
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package shell

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

const (
	bufferSize  = 1 << 16
	checkStatus = `if [ $? -eq 0 ]; then
	echo OK
else
	echo FAILED
fi`
)

// Bash is api for bash procces
type Bash struct {
	Dir       string
	Env       []string
	errCh     chan error
	once      sync.Once
	resources []io.Closer
	stdin     io.Writer
	outCh     chan string
	ctx       context.Context
	cancel    context.CancelFunc
	cmd       *exec.Cmd
}

// Close closses current bash process and all used resources
func (b *Bash) Close() {
	b.once.Do(b.init)
	b.cancel()
	_, _ = b.stdin.Write([]byte("exit 0\n"))
	_ = b.cmd.Wait()
	for _, r := range b.resources {
		_ = r.Close()
	}
}

func (b *Bash) init() {
	b.ctx, b.cancel = context.WithCancel(context.Background())
	b.errCh = make(chan error)
	b.outCh = make(chan string)
	p, err := exec.LookPath("bash")
	if err != nil {
		panic(err.Error())
	}
	if len(b.Env) == 0 {
		b.Env = os.Environ()
	}
	b.cmd = &exec.Cmd{
		Dir:  b.Dir,
		Env:  b.Env,
		Path: p,
	}

	stderr, err := b.cmd.StderrPipe()
	if err != nil {
		panic(err.Error())
	}
	b.resources = append(b.resources, stderr)

	stdin, err := b.cmd.StdinPipe()
	if err != nil {
		panic(err.Error())
	}
	b.resources = append(b.resources, stdin)
	b.stdin = stdin

	stdout, err := b.cmd.StdoutPipe()
	if err != nil {
		panic(err.Error())
	}
	b.resources = append(b.resources, stdout)

	err = b.cmd.Start()
	if err != nil {
		panic(err.Error())
	}

	go b.stderrHandler(stderr)
	go b.stdoutHandler(stdout)
}

func (b *Bash) stderrHandler(stderr io.Reader) {
	var buffer []byte = make([]byte, bufferSize)
	for b.ctx.Err() == nil {
		n, err := stderr.Read(buffer)
		if err != nil {
			return
		}
		b.errCh <- errors.New(string(buffer[:n]))
	}
}

func (b *Bash) stdoutHandler(stdout io.Reader) {
	var output string
	var buffer []byte = make([]byte, bufferSize)
	cur := 0
	for b.ctx.Err() == nil {
		n, err := stdout.Read(buffer[cur:])
		if err != nil {
			return
		}
		r := strings.TrimSpace(string(buffer[:cur+n]))
		if strings.HasSuffix(r, "OK") {
			if len(r) > 2 {
				output = r[:len(r)-len("\nOK")]
			}
			b.outCh <- output
			output = ""
			cur = 0
			continue
		}
		if strings.HasSuffix(r, "FAILED") {
			b.errCh <- errors.New("command has failed")
			cur = 0
			continue
		}
		cur += n
		if cur == bufferSize {
			cur = 0
		}
	}
}

// Run runs the cmd. Returs stdout and stderror as a result.
func (b *Bash) Run(s string) (output string, err error) {
	b.once.Do(b.init)

	if b.ctx.Err() != nil {
		return "", b.ctx.Err()
	}

	_, err = b.stdin.Write([]byte(s + "\n"))
	if err != nil {
		return "", err
	}

	_, err = b.stdin.Write([]byte(checkStatus + "\n"))
	if err != nil {
		return "", err
	}

	select {
	case err = <-b.errCh:
		return "", err
	case output = <-b.outCh:
		return output, nil
	case <-b.ctx.Done():
		return "", b.ctx.Err()
	}
}
