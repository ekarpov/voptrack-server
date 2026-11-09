package backupreplication

import "syscall"

const syscallNoFollowNonblock = syscall.O_NOFOLLOW | syscall.O_NONBLOCK
