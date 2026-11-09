#define _GNU_SOURCE
#include <sys/ptrace.h>
#include <linux/ptrace.h>
#include <sys/user.h>
#include <sys/wait.h>
#include <sys/syscall.h>
#include <unistd.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <errno.h>
#include <string.h>
#include <time.h>
#include <sys/stat.h>
static pid_t child;
static long long milliseconds(void){struct timespec t;clock_gettime(CLOCK_MONOTONIC,&t);return (long long)t.tv_sec*1000+t.tv_nsec/1000000;}
static void readmode(const char *path,char mode[128]){strcpy(mode,"off");FILE *f=fopen(path,"r");if(f){if(!fgets(mode,128,f))strcpy(mode,"off");fclose(f);}}

static void stop(int sig){if(child>0)kill(-child,sig);}
struct thread {pid_t id;int inject;} threads[2048];
static struct thread *thread(pid_t id){for(int i=0;i<2048;i++)if(threads[i].id==id)return &threads[i];for(int i=0;i<2048;i++)if(!threads[i].id){threads[i].id=id;return &threads[i];}fprintf(stderr,"persistfault thread capacity\n");exit(95);}
int main(int argc,char **argv){
 if(argc<4){fprintf(stderr,"usage: trace CONTROL EXACT_JOURNAL_DIRECTORY COMMAND ARGS...\n");return 2;}
 child=fork();if(child<0)return 90;
 if(!child){setpgid(0,0);if(ptrace(PTRACE_TRACEME,0,0,0)<0){perror("ptrace unavailable");_exit(93);}raise(SIGSTOP);execvp(argv[3],argv+3);_exit(94);}
 signal(SIGTERM,stop);signal(SIGINT,stop);int status;if(waitpid(child,&status,0)<0||!WIFSTOPPED(status))return 93;
 long opts=PTRACE_O_TRACESYSGOOD|PTRACE_O_TRACECLONE|PTRACE_O_TRACEFORK|PTRACE_O_TRACEVFORK|PTRACE_O_EXITKILL;
 if(ptrace(PTRACE_SETOPTIONS,child,0,opts)<0){perror("ptrace options unavailable");kill(-child,SIGKILL);waitpid(child,&status,0);return 93;}
 thread(child);if(ptrace(PTRACE_SYSCALL,child,0,0)<0)return 93;fprintf(stderr,"persistfault ptrace ready\n");fflush(stderr);
 int seen=0,failed=0,exitcode=0;char previous[128]="";
 pid_t parked=0;char parked_nonce[80]="";long long parked_at=0;long pause_ms=90000;
 const char *bound=getenv("VOPTRACK_FAULT_PAUSE_MS");if(bound){long n=strtol(bound,NULL,10);if(n>0&&n<=90000)pause_ms=n;}

 for(;;){
 if(parked){char mode[128],action[20],nonce[80];readmode(argv[1],mode);int release=sscanf(mode,"%19s %79s",action,nonce)==2&&!strcmp(action,"release")&&!strcmp(nonce,parked_nonce);int expired=milliseconds()-parked_at>=pause_ms;if(release||expired){fprintf(stderr,"persistfault stage %s\n",expired?"timeout":"released");fflush(stderr);if(ptrace(PTRACE_SYSCALL,parked,0,0)<0&&errno!=ESRCH){kill(-child,SIGKILL);return 95;}parked=0;}}
 pid_t tid=waitpid(-1,&status,__WALL|(parked?WNOHANG:0));if(tid==0){usleep(1000);continue;}if(tid<0){if(errno==EINTR)continue;if(errno==ECHILD)break;return 95;}struct thread *t=thread(tid);
 if(WIFEXITED(status)||WIFSIGNALED(status)){if(tid==parked)parked=0;t->id=0;if(tid==child)exitcode=WIFEXITED(status)?WEXITSTATUS(status):128+WTERMSIG(status);continue;}
 if(!WIFSTOPPED(status))continue;
 int sig=WSTOPSIG(status),deliver=0;
 if(sig==(SIGTRAP|0x80)){
  struct ptrace_syscall_info info={0};if(ptrace(PTRACE_GET_SYSCALL_INFO,tid,sizeof info,&info)<0){perror("ptrace syscall info unavailable");kill(-child,SIGKILL);return 95;}
  if(info.op==PTRACE_SYSCALL_INFO_ENTRY&&(info.entry.nr==SYS_fsync||info.entry.nr==SYS_fdatasync)){
   char link[100],path[4096];snprintf(link,sizeof link,"/proc/%d/fd/%llu",tid,(unsigned long long)info.entry.args[0]);ssize_t n=readlink(link,path,sizeof path-1);if(n>=0)path[n]=0;else path[0]=0;
   size_t root=strlen(argv[2]);int match=strlen(path)>root+10&&!strncmp(path,argv[2],root)&&!strncmp(path+root,"/.journal-",10)&&strchr(path+root+1,'/')==NULL;
   char mode[128];readmode(argv[1],mode);if(strcmp(mode,previous)){snprintf(previous,sizeof previous,"%s",mode);seen=0;failed=0;}
   char pause_action[20],pause_nonce[80];struct stat metadata;
   int stage=strlen(path)>root+7&&!strncmp(path,argv[2],root)&&!strncmp(path+root,"/stage-",7)&&strchr(path+root+1,'/')==NULL&&stat(link,&metadata)==0&&S_ISDIR(metadata.st_mode);
   if(stage&&!parked&&!failed&&sscanf(mode,"%19s %79s",pause_action,pause_nonce)==2&&!strcmp(pause_action,"pause-stage")){parked=tid;parked_at=milliseconds();snprintf(parked_nonce,sizeof parked_nonce,"%s",pause_nonce);failed=1;fprintf(stderr,"persistfault stage paused\n");fflush(stderr);continue;}
   int target=0;char tag[80],action[16];int parsed=sscanf(mode,"%15s %d %79s",action,&target,tag);int directory=!strcmp(path,argv[2]);int selected=parsed==3&&((!strcmp(action,"fail")&&match)||(!strcmp(action,"fail-dir")&&directory));if(selected&&target>0){seen++;if(seen==target&&!failed){struct user_regs_struct regs;if(ptrace(PTRACE_GETREGS,tid,0,&regs)<0)return 95;regs.orig_rax=-1;if(ptrace(PTRACE_SETREGS,tid,0,&regs)<0)return 95;t->inject=1;failed=1;fprintf(stderr,"persistfault EIO journal-%s sync ordinal=%d\n",directory?"directory":"temp",seen);fflush(stderr);}}
  }else if(info.op==PTRACE_SYSCALL_INFO_EXIT&&t->inject){struct user_regs_struct regs;if(ptrace(PTRACE_GETREGS,tid,0,&regs)<0)return 95;regs.rax=-EIO;if(ptrace(PTRACE_SETREGS,tid,0,&regs)<0)return 95;t->inject=0;}
 }else if(sig!=SIGSTOP&&sig!=SIGTRAP)deliver=sig;
 if(ptrace(PTRACE_SYSCALL,tid,0,deliver)<0&&errno!=ESRCH){perror("ptrace resume");kill(-child,SIGKILL);return 95;}
 }
 return exitcode;
}
