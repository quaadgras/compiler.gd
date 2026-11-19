// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

// regex.h (internal/c23/regex), termios.h (internal/c23/termios, which
// also has unistd.h's tcgetpgrp and tcsetpgrp) and sys/ttydefaults.h.

func init() {
	sysHeaders["regex.h"] = `#ifndef __CC_REGEX_H
#define __CC_REGEX_H
#include <stddef.h>
typedef int regoff_t;
typedef struct re_pattern_buffer {
	size_t re_nsub;
	void *__opaque, *__padding[4];
	size_t __nsub2;
	char __padding2;
} regex_t;
typedef struct {
	regoff_t rm_so;
	regoff_t rm_eo;
} regmatch_t;
#define REG_EXTENDED 1
#define REG_ICASE 2
#define REG_NEWLINE 4
#define REG_NOSUB 8
#define REG_NOTBOL 1
#define REG_NOTEOL 2
#define REG_STARTEND 4
#define REG_NOERROR 0
#define REG_OK 0
#define REG_NOMATCH 1
#define REG_BADPAT 2
#define REG_ECOLLATE 3
#define REG_ECTYPE 4
#define REG_EESCAPE 5
#define REG_ESUBREG 6
#define REG_EBRACK 7
#define REG_EPAREN 8
#define REG_EBRACE 9
#define REG_BADBR 10
#define REG_ERANGE 11
#define REG_ESPACE 12
#define REG_BADRPT 13
#define REG_EEND 14
#define REG_ESIZE 15
#define REG_ERPAREN 16
#ifndef RE_DUP_MAX
#define RE_DUP_MAX 0x7fff
#endif
int regcomp(regex_t *restrict preg, const char *restrict pattern, int cflags);
int regexec(const regex_t *restrict preg, const char *restrict string, size_t nmatch, regmatch_t *restrict pmatch, int eflags);
size_t regerror(int errcode, const regex_t *restrict preg, char *restrict errbuf, size_t errbuf_size);
void regfree(regex_t *preg);
#endif
`
	sysHeaders["termios.h"] = `#ifndef __CC_TERMIOS_H
#define __CC_TERMIOS_H
#include <sys/types.h>
typedef unsigned char cc_t;
typedef unsigned int speed_t;
typedef unsigned int tcflag_t;
#define NCCS 32
struct termios {
	tcflag_t c_iflag;
	tcflag_t c_oflag;
	tcflag_t c_cflag;
	tcflag_t c_lflag;
	cc_t c_line;
	cc_t c_cc[NCCS];
	speed_t c_ispeed;
	speed_t c_ospeed;
};
#define _HAVE_STRUCT_TERMIOS_C_ISPEED 1
#define _HAVE_STRUCT_TERMIOS_C_OSPEED 1
#define VINTR 0
#define VQUIT 1
#define VERASE 2
#define VKILL 3
#define VEOF 4
#define VTIME 5
#define VMIN 6
#define VSWTC 7
#define VSTART 8
#define VSTOP 9
#define VSUSP 10
#define VEOL 11
#define VREPRINT 12
#define VDISCARD 13
#define VWERASE 14
#define VLNEXT 15
#define VEOL2 16
#define IGNBRK 0000001
#define BRKINT 0000002
#define IGNPAR 0000004
#define PARMRK 0000010
#define INPCK 0000020
#define ISTRIP 0000040
#define INLCR 0000100
#define IGNCR 0000200
#define ICRNL 0000400
#define IUCLC 0001000
#define IXON 0002000
#define IXANY 0004000
#define IXOFF 0010000
#define IMAXBEL 0020000
#define IUTF8 0040000
#define OPOST 0000001
#define OLCUC 0000002
#define ONLCR 0000004
#define OCRNL 0000010
#define ONOCR 0000020
#define ONLRET 0000040
#define OFILL 0000100
#define OFDEL 0000200
#define NLDLY 0000400
#define NL0 0000000
#define NL1 0000400
#define CRDLY 0003000
#define CR0 0000000
#define CR1 0001000
#define CR2 0002000
#define CR3 0003000
#define TABDLY 0014000
#define TAB0 0000000
#define TAB1 0004000
#define TAB2 0010000
#define TAB3 0014000
#define XTABS 0014000
#define BSDLY 0020000
#define BS0 0000000
#define BS1 0020000
#define VTDLY 0040000
#define VT0 0000000
#define VT1 0040000
#define FFDLY 0100000
#define FF0 0000000
#define FF1 0100000
#define B0 0000000
#define B50 0000001
#define B75 0000002
#define B110 0000003
#define B134 0000004
#define B150 0000005
#define B200 0000006
#define B300 0000007
#define B600 0000010
#define B1200 0000011
#define B1800 0000012
#define B2400 0000013
#define B4800 0000014
#define B9600 0000015
#define B19200 0000016
#define B38400 0000017
#define EXTA 0000016
#define EXTB 0000017
#define B57600 0010001
#define B115200 0010002
#define B230400 0010003
#define B460800 0010004
#define B500000 0010005
#define B576000 0010006
#define B921600 0010007
#define B1000000 0010010
#define B1152000 0010011
#define B1500000 0010012
#define B2000000 0010013
#define B2500000 0010014
#define B3000000 0010015
#define B3500000 0010016
#define B4000000 0010017
#define CBAUD 0010017
#define CBAUDEX 0010000
#define CIBAUD 002003600000
#define CMSPAR 010000000000
#define CRTSCTS 020000000000
#define CSIZE 0000060
#define CS5 0000000
#define CS6 0000020
#define CS7 0000040
#define CS8 0000060
#define CSTOPB 0000100
#define CREAD 0000200
#define PARENB 0000400
#define PARODD 0001000
#define HUPCL 0002000
#define CLOCAL 0004000
#define ISIG 0000001
#define ICANON 0000002
#define XCASE 0000004
#define ECHO 0000010
#define ECHOE 0000020
#define ECHOK 0000040
#define ECHONL 0000100
#define NOFLSH 0000200
#define TOSTOP 0000400
#define ECHOCTL 0001000
#define ECHOPRT 0002000
#define ECHOKE 0004000
#define FLUSHO 0010000
#define PENDIN 0040000
#define IEXTEN 0100000
#define EXTPROC 0200000
#define TCOOFF 0
#define TCOON 1
#define TCIOFF 2
#define TCION 3
#define TCIFLUSH 0
#define TCOFLUSH 1
#define TCIOFLUSH 2
#define TCSANOW 0
#define TCSADRAIN 1
#define TCSAFLUSH 2
speed_t cfgetospeed(const struct termios *t);
speed_t cfgetispeed(const struct termios *t);
int cfsetospeed(struct termios *t, speed_t speed);
int cfsetispeed(struct termios *t, speed_t speed);
int cfsetspeed(struct termios *t, speed_t speed);
void cfmakeraw(struct termios *t);
int tcgetattr(int fd, struct termios *t);
int tcsetattr(int fd, int act, const struct termios *t);
int tcsendbreak(int fd, int duration);
int tcdrain(int fd);
int tcflush(int fd, int queue);
int tcflow(int fd, int action);
pid_t tcgetsid(int fd);
#endif
`
	sysHeaders["sys/ttydefaults.h"] = `#ifndef __CC_SYS_TTYDEFAULTS_H
#define __CC_SYS_TTYDEFAULTS_H
#define TTYDEF_IFLAG (BRKINT | ISTRIP | ICRNL | IMAXBEL | IXON | IXANY)
#define TTYDEF_OFLAG (OPOST | ONLCR | XTABS)
#define TTYDEF_LFLAG (ECHO | ICANON | ISIG | IEXTEN | ECHOE | ECHOKE | ECHOCTL)
#define TTYDEF_CFLAG (CREAD | CS7 | PARENB | HUPCL)
#define TTYDEF_SPEED (B9600)
#define CTRL(x) ((x) & 037)
#define CEOF CTRL('d')
#define CEOL '\0'
#define CSTATUS '\0'
#define CERASE 0177
#define CINTR CTRL('c')
#define CKILL CTRL('u')
#define CMIN 1
#define CQUIT 034
#define CSUSP CTRL('z')
#define CTIME 0
#define CDSUSP CTRL('y')
#define CSTART CTRL('q')
#define CSTOP CTRL('s')
#define CLNEXT CTRL('v')
#define CDISCARD CTRL('o')
#define CWERASE CTRL('w')
#define CREPRINT CTRL('r')
#define CEOT CEOF
#define CBRK CEOL
#define CRPRNT CREPRINT
#define CFLUSH CDISCARD
#endif
`
	for k, v := range map[string]string{
		// regex.h
		"regcomp": "regex.Regcomp", "regexec": "regex.Regexec", "regerror": "regex.Regerror", "regfree": "regex.Regfree",
		// termios.h
		"tcgetattr": "termios.Tcgetattr", "tcsetattr": "termios.Tcsetattr", "cfmakeraw": "termios.Cfmakeraw",
		"cfgetispeed": "termios.Cfgetispeed", "cfgetospeed": "termios.Cfgetospeed",
		"cfsetispeed": "termios.Cfsetispeed", "cfsetospeed": "termios.Cfsetospeed", "cfsetspeed": "termios.Cfsetspeed",
		"tcflush": "termios.Tcflush", "tcdrain": "termios.Tcdrain", "tcflow": "termios.Tcflow",
		"tcsendbreak": "termios.Tcsendbreak", "tcgetsid": "termios.Tcgetsid",
		// unistd.h
		"tcgetpgrp": "termios.Tcgetpgrp", "tcsetpgrp": "termios.Tcsetpgrp",
	} {
		libc[k] = v
	}
}
