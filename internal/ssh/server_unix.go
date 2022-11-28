//go:build !windows
// +build !windows

package ssh

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"github.com/creack/pty"
	"github.com/gliderlabs/ssh"
)

func execCmd(s ssh.Session, cmd exec.Cmd, uid, gid uint64) {

	euid := os.Geteuid()
	loginCmd, _ := exec.LookPath("login")

	sysProcAttr := &syscall.SysProcAttr{}

	if len(s.Command()) > 0 {
		sysProcAttr.Credential = &syscall.Credential{
			Uid:         uint32(uid),
			Gid:         uint32(gid),
			NoSetGroups: true,
		}

		cmd.Args = append(cmd.Args, "-c", s.RawCommand())
	} else {
		if euid == 0 {
			if loginCmd != "" {
				cmd.Path = loginCmd
				cmd.Args = append([]string{loginCmd, "-p", "-h", "Border0", "-f", s.User()}, cmd.Args...)
			}
		} else {
			sysProcAttr.Credential = &syscall.Credential{
				Uid:         uint32(uid),
				Gid:         uint32(gid),
				NoSetGroups: true,
			}

			cmd.Args = []string{fmt.Sprintf("-%s", cmd.Args[0])}
		}
	}

	ptyReq, winCh, isPty := s.Pty()

	if isPty {
		cmd.Env = append(cmd.Env, fmt.Sprintf("TERM=%s", ptyReq.Term))
		sysProcAttr.Setsid = true
		sysProcAttr.Setctty = true

		f, err := pty.StartWithAttrs(&cmd, &pty.Winsize{}, sysProcAttr)
		if err != nil {
			log.Println(err)
			return
		}

		go func() {
			for win := range winCh {
				setWinsize(f, win.Width, win.Height)
			}
		}()

		done := make(chan bool, 2)

		go func() {
			io.Copy(f, s)
			done <- true
		}()

		go func() {
			io.Copy(s, f)
			done <- true
		}()

		go func() {
			cmd.Wait()
			done <- true
		}()

		select {
		case <-done:
		case <-s.Context().Done():
		}

		if cmd.ProcessState == nil {
			cmd.Process.Signal(syscall.SIGHUP)
		}

	} else {
		sysProcAttr.Setsid = true
		cmd.SysProcAttr = sysProcAttr

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			log.Printf("failed to set stdout: %v\n", err)
			return
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			log.Printf("failed to set stderr: %v\n", err)
			return
		}
		stdin, err := cmd.StdinPipe()
		if err != nil {
			log.Printf("failed to set stdin: %v\n", err)
			return
		}

		wg := &sync.WaitGroup{}
		wg.Add(2)
		if err = cmd.Start(); err != nil {
			log.Printf("failed to start command %v\n", err)
			return
		}
		go func() {
			defer stdin.Close()
			if _, err := io.Copy(stdin, s); err != nil {
				log.Printf("failed to write to session %s\n", err)
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := io.Copy(s, stdout); err != nil {
				log.Printf("failed to write to stdout %s\n", err)
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := io.Copy(s.Stderr(), stderr); err != nil {
				log.Printf("failed to write from stderr%s\n", err)
			}
		}()

		wg.Wait()
		cmd.Wait()

	}
}

func setWinsize(f *os.File, w, h int) {
	syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(syscall.TIOCSWINSZ),
		uintptr(unsafe.Pointer(&struct{ h, w, x, y uint16 }{uint16(h), uint16(w), 0, 0})))
}
