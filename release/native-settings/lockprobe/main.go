// lockprobe inspects only a Voptrack-owned inode lock in disposable gates.
package main

import (
	"os"
	"syscall"
)

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	f, e := os.OpenFile(os.Args[1], os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		os.Exit(2)
	}
	defer f.Close()
	e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if e == syscall.EWOULDBLOCK || e == syscall.EAGAIN {
		return
	}
	if e == nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	}
	os.Exit(1)
}
