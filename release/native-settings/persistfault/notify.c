#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <linux/filter.h>
#include <linux/seccomp.h>
#include <stddef.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/prctl.h>
#include <sys/socket.h>
#include <sys/syscall.h>
#include <sys/wait.h>
#include <unistd.h>
#include <signal.h>
#include <poll.h>
static pid_t child;
static void stop(int s){if(child>0)kill(child,s);}
static void passfd(int sock,int fd){char b=0;struct iovec io={&b,1};char c[CMSG_SPACE(sizeof(int))]={0};struct msghdr m={.msg_iov=&io,.msg_iovlen=1,.msg_control=c,.msg_controllen=sizeof c};struct cmsghdr *h=CMSG_FIRSTHDR(&m);h->cmsg_level=SOL_SOCKET;h->cmsg_type=SCM_RIGHTS;h->cmsg_len=CMSG_LEN(sizeof(int));memcpy(CMSG_DATA(h),&fd,sizeof fd);if(sendmsg(sock,&m,0)!=1)_exit(91);}
static int recvfd(int sock){char b,c[CMSG_SPACE(sizeof(int))];struct iovec io={&b,1};struct msghdr m={.msg_iov=&io,.msg_iovlen=1,.msg_control=c,.msg_controllen=sizeof c};if(recvmsg(sock,&m,0)!=1)return -1;struct cmsghdr *h=CMSG_FIRSTHDR(&m);int fd=-1;if(h)memcpy(&fd,CMSG_DATA(h),sizeof fd);return fd;}
int main(int argc,char **argv){
 if(argc<4){fprintf(stderr,"usage: notify CONTROL EXACT_JOURNAL_DIRECTORY COMMAND ARGS...\n");return 2;}
 int sv[2];if(socketpair(AF_UNIX,SOCK_STREAM,0,sv))return 90;
 child=fork();if(child<0)return 90;
 if(!child){close(sv[0]);struct sock_filter ins[]={BPF_STMT(BPF_LD|BPF_W|BPF_ABS,offsetof(struct seccomp_data,nr)),BPF_JUMP(BPF_JMP|BPF_JEQ|BPF_K,SYS_fsync,2,0),BPF_JUMP(BPF_JMP|BPF_JEQ|BPF_K,SYS_fdatasync,1,0),BPF_STMT(BPF_RET|BPF_K,SECCOMP_RET_ALLOW),BPF_STMT(BPF_RET|BPF_K,SECCOMP_RET_USER_NOTIF)};struct sock_fprog p={sizeof ins/sizeof ins[0],ins};if(prctl(PR_SET_NO_NEW_PRIVS,1,0,0,0))_exit(92);int fd=syscall(SYS_seccomp,SECCOMP_SET_MODE_FILTER,SECCOMP_FILTER_FLAG_NEW_LISTENER,&p);if(fd<0){perror("seccomp USER_NOTIF unavailable");_exit(93);}passfd(sv[1],fd);close(fd);close(sv[1]);execvp(argv[3],argv+3);_exit(94);}
 close(sv[1]);int listener=recvfd(sv[0]);close(sv[0]);if(listener<0){int st;waitpid(child,&st,0);return 93;}signal(SIGTERM,stop);signal(SIGINT,stop);fprintf(stderr,"persistfault ready\n");fflush(stderr);
 int seen=0,failed=0;char previous[128]="";
 for(;;){int status;if(waitpid(child,&status,WNOHANG)==child){close(listener);return WIFEXITED(status)?WEXITSTATUS(status):128+WTERMSIG(status);}struct pollfd ready={listener,POLLIN,0};int polled=poll(&ready,1,100);if(polled<0&&errno==EINTR)continue;if(polled<0)break;if(polled==0)continue;struct seccomp_notif req={0};struct seccomp_notif_resp resp={0};if(ioctl(listener,SECCOMP_IOCTL_NOTIF_RECV,&req)){if(errno==EINTR)continue;break;}resp.id=req.id;resp.flags=SECCOMP_USER_NOTIF_FLAG_CONTINUE;
  char link[100],path[4096];snprintf(link,sizeof link,"/proc/%u/fd/%llu",req.pid,(unsigned long long)req.data.args[0]);ssize_t n=readlink(link,path,sizeof path-1);if(n>=0)path[n]=0;else path[0]=0;
  size_t root=strlen(argv[2]);int match=!strncmp(path,argv[2],root)&&!strncmp(path+root,"/.journal-",10)&&strchr(path+root+1,'/')==NULL;
  char mode[128]="off";FILE *f=fopen(argv[1],"r");if(f){if(!fgets(mode,sizeof mode,f))strcpy(mode,"off");fclose(f);}if(strcmp(mode,previous)){snprintf(previous,sizeof previous,"%s",mode);seen=0;failed=0;}
  int target=0;char tag[80];if(match&&sscanf(mode,"fail %d %79s",&target,tag)==2&&target>0){seen++;if(seen==target&&!failed){resp.flags=0;resp.error=-EIO;failed=1;fprintf(stderr,"persistfault EIO journal-temp sync ordinal=%d\n",seen);fflush(stderr);}}
  if(ioctl(listener,SECCOMP_IOCTL_NOTIF_SEND,&resp)&&errno!=ENOENT)break;
 }
 close(listener);int st;while(waitpid(child,&st,0)<0&&errno==EINTR){}return WIFEXITED(st)?WEXITSTATUS(st):128+WTERMSIG(st);
}
