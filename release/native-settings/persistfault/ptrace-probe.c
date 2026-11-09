#include <sys/ptrace.h>
#include <sys/wait.h>
#include <unistd.h>
#include <signal.h>
#include <stdio.h>
int main(void){pid_t p=fork();if(p<0)return 2;if(p==0){if(ptrace(PTRACE_TRACEME,0,0,0)<0){perror("PTRACE_TRACEME unavailable");_exit(93);}raise(SIGSTOP);_exit(0);}int s;if(waitpid(p,&s,0)<0)return 2;if(WIFSTOPPED(s)){if(ptrace(PTRACE_CONT,p,0,0)<0){perror("PTRACE_CONT unavailable");kill(p,SIGKILL);waitpid(p,&s,0);return 93;}if(waitpid(p,&s,0)<0)return 2;}return WIFEXITED(s)?WEXITSTATUS(s):94;}
