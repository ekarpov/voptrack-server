#define _GNU_SOURCE
#include <unistd.h>
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <errno.h>
int main(int argc,char **argv){if(argc!=3)return 2;char path[4096];snprintf(path,sizeof path,"%s/unrelated",argv[1]);int fd=open(path,O_CREAT|O_RDWR,0600);if(fd<0||write(fd,"x",1)!=1||fsync(fd))return 3;close(fd);unlink(path);snprintf(path,sizeof path,"%s/.journal-XXXXXX",argv[1]);fd=mkstemp(path);if(fd<0)return 4;int expected=atoi(argv[2]);for(int n=1;n<=3;n++){errno=0;int r=fsync(fd);if(n==expected){if(r!=-1||errno!=EIO)return 5;}else if(r)return 6;}close(fd);unlink(path);puts("sync probe passed");return 0;}
