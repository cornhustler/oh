// Released under an MIT license. See LICENSE.

// +build linux darwin dragonfly freebsd openbsd netbsd solaris

package task

import (
	. "github.com/michaelmacinnis/oh/pkg/cell"
	"golang.org/x/sys/unix"
	"os"
	"os/signal"
	"syscall"
)

type notification struct {
	pid    int
	status syscall.WaitStatus
}

type registration struct {
	pid int
	cb  chan notification
}

var (
	done0    chan Cell
	eval0    chan message
	incoming chan os.Signal
	register chan registration
)

func broker(cli ui) {
	for task0.Stack != Null {
		for reading := true; reading; {
			select {
			case <-incoming: // Discard signals.
			case c := <-eval0:
				task0.Eval <- c
				reading = false
			}
		}

		var v Cell
		for evaluating := true; evaluating; {
			prev := task0

			select {
			case sig := <-incoming: // Handle signals.
				switch sig {
				case unix.SIGTSTP:
					task0.Suspend()

					last := 0
					jobsl.RLock()
					for k := range jobs {
						if k > last {
							last = k
						}
					}
					jobsl.RUnlock()
					last++

					jobsl.Lock()
					jobs[last] = task0
					jobsl.Unlock()

				case unix.SIGINT:
					task0.Stop()
				}

				launchForegroundTask(cli)

			case v = <-task0.Done:
				if task0 != prev {
					continue
				}
			}

			evaluating = false
		}

		done0 <- v
	}
}

func evaluate(c Cell, f string, l int, p string) (Cell, bool) {
	eval0 <- message{cmd: c, file: f, line: l, problem: p}
	r := <-done0

	task0.Job.Command = ""
	task0.Job.Group = 0

	return r, task0.Stack != Null
}

func exit(c Cell) {
	if c == Null {
		os.Exit(0)
	}

	a, ok := c.(Atom)
	if !ok {
		os.Exit(1)
	}

	os.Exit(int(a.Status()))
}

func init() {
	done0 = make(chan Cell)
	eval0 = make(chan message)

	active := make(chan bool)
	notify := make(chan notification)
	register = make(chan registration)

	go monitor(active, notify)
	go registrar(active, notify)
}

func initPlatformSpecific() {
	scope0.DefineBuiltin("_umask_", func(t *Task, args Cell) bool {
		nmask := int64(0)
		if args != Null {
			nmask = Car(args).(Atom).Int()
		}

		omask := unix.Umask(int(nmask))

		if nmask == 0 {
			unix.Umask(omask)
		}

		return t.Return(NewInteger(int64(omask)))
	})
}

func initSignalHandling(cli ui) {
	signal.Ignore(unix.SIGTTOU, unix.SIGTTIN)

	signals := []os.Signal{unix.SIGINT, unix.SIGTSTP}
	incoming = make(chan os.Signal, len(signals))

	signal.Notify(incoming, signals...)

	go broker(cli)
}

func monitor(active chan bool, notify chan notification) {
	for {
		monitoring := <-active
		for monitoring {
			var rusage syscall.Rusage
			var status syscall.WaitStatus
			options := syscall.WUNTRACED
			pid, err := syscall.Wait4(-1, &status, options, &rusage)
			if err != nil {
				println("Wait4:", err.Error())
			}
			if pid <= 0 {
				break
			}

			if status.Stopped() {
				if pid == task0.Job.Group {
					incoming <- unix.SIGTSTP
				}
				continue
			}

			if status.Signaled() {
				if status.Signal() == unix.SIGINT &&
					pid == task0.Job.Group {
					incoming <- unix.SIGINT
				}
				status += 128
			}

			notify <- notification{pid, status}
			monitoring = <-active
		}
	}
}

func registrar(active chan bool, notify chan notification) {
	preregistered := make(map[int]notification)
	registered := make(map[int]registration)
	for {
		select {
		case n := <-notify:
			r, ok := registered[n.pid]
			if ok {
				r.cb <- n
				delete(registered, n.pid)
			} else {
				preregistered[n.pid] = n
			}
			active <- len(registered) != 0
		case r := <-register:
			if n, ok := preregistered[r.pid]; ok {
				r.cb <- n
				delete(preregistered, r.pid)
			} else {
				registered[r.pid] = r
				if len(registered) == 1 {
					active <- true
				}
			}
		}
	}
}

func status(proc *os.Process) *Status {
	response := make(chan notification)
	register <- registration{proc.Pid, response}

	return NewStatus(int64((<-response).status.ExitStatus()))
}
