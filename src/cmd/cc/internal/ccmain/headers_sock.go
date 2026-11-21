// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

// The headers of sockets and of poll and select: internal/c23/sys/socket,
// netdb, arpa/inet, netinet/in and net/if, of which only the first two
// link net (libcpkg.go); poll and select's packages.

func init() {
	for name, text := range sockHeaders {
		sysHeaders[name] = text
	}
	for c, goName := range sockLibc {
		libc[c] = goName
	}
}

var sockLibc = map[string]string{
	"socket": "sys/socket.Socket", "socketpair": "sys/socket.Socketpair", "bind": "sys/socket.Bind",
	"listen": "sys/socket.Listen", "accept": "sys/socket.Accept", "accept4": "sys/socket.Accept4",
	"connect": "sys/socket.Connect", "shutdown": "sys/socket.Shutdown",
	"send": "sys/socket.Send", "sendto": "sys/socket.Sendto", "sendmsg": "sys/socket.Sendmsg",
	"recv": "sys/socket.Recv", "recvfrom": "sys/socket.Recvfrom", "recvmsg": "sys/socket.Recvmsg",
	"getsockname": "sys/socket.Getsockname", "getpeername": "sys/socket.Getpeername",
	"getsockopt": "sys/socket.Getsockopt", "setsockopt": "sys/socket.Setsockopt",
	"htons": "netinet/in.Htons", "ntohs": "netinet/in.Ntohs", "htonl": "netinet/in.Htonl", "ntohl": "netinet/in.Ntohl",
	"inet_aton": "arpa/inet.InetAton", "inet_addr": "arpa/inet.InetAddr", "__cc_inet_ntoa": "arpa/inet.InetNtoa",
	"inet_pton": "arpa/inet.InetPton", "inet_ntop": "arpa/inet.InetNtop",
	"getaddrinfo": "netdb.Getaddrinfo", "freeaddrinfo": "netdb.Freeaddrinfo",
	"gai_strerror": "netdb.GaiStrerror", "getnameinfo": "netdb.Getnameinfo",
	"gethostbyname": "netdb.Gethostbyname", "gethostbyname2": "netdb.Gethostbyname2",
	"gethostbyaddr": "netdb.Gethostbyaddr", "hstrerror": "netdb.HstrError",
	"__cc_h_errno":  "netdb.HErrnoLocation",
	"getservbyname": "netdb.Getservbyname", "getprotobyname": "netdb.Getprotobyname",
	"getprotobynumber": "netdb.Getprotobynumber",
	"if_nametoindex":   "net/if.IfNametoindex", "if_indextoname": "net/if.IfIndextoname",
	"poll": "poll.Poll", "select": "sys/select.Select", "pselect": "sys/select.Pselect",
}

var sockHeaders = map[string]string{
	"sys/socket.h": `#ifndef __CC_SYS_SOCKET_H
#define __CC_SYS_SOCKET_H
#include <stddef.h>
#include <sys/types.h>
typedef unsigned int socklen_t;
typedef unsigned short sa_family_t;
struct sockaddr {
	sa_family_t sa_family;
	char sa_data[14];
};
struct sockaddr_storage {
	sa_family_t ss_family;
	char __ss_padding[128 - sizeof(unsigned short) - sizeof(unsigned long)];
	unsigned long __ss_align;
};
#ifndef __CC_IOVEC
#define __CC_IOVEC
struct iovec {
	void *iov_base;
	size_t iov_len;
};
#endif
struct msghdr {
	void *msg_name;
	socklen_t msg_namelen;
	struct iovec *msg_iov;
	size_t msg_iovlen;
	void *msg_control;
	size_t msg_controllen;
	int msg_flags;
};
struct cmsghdr {
	size_t cmsg_len;
	int cmsg_level;
	int cmsg_type;
};
#define CMSG_FIRSTHDR(m) ((size_t)(m)->msg_controllen >= sizeof(struct cmsghdr) ? (struct cmsghdr *)(m)->msg_control : (struct cmsghdr *)0)
#define CMSG_NXTHDR(m, c) ((size_t)((unsigned char *)(c) + CMSG_ALIGN((c)->cmsg_len) - (unsigned char *)(m)->msg_control) + sizeof(struct cmsghdr) <= (size_t)(m)->msg_controllen ? (struct cmsghdr *)((unsigned char *)(c) + CMSG_ALIGN((c)->cmsg_len)) : (struct cmsghdr *)0)
#define SCM_RIGHTS 1
#define CMSG_ALIGN(n) (((n) + sizeof(size_t) - 1) & ~(sizeof(size_t) - 1))
#define CMSG_DATA(c) ((unsigned char *)((struct cmsghdr *)(c) + 1))
#define CMSG_SPACE(n) (CMSG_ALIGN(n) + CMSG_ALIGN(sizeof(struct cmsghdr)))
#define CMSG_LEN(n) (CMSG_ALIGN(sizeof(struct cmsghdr)) + (n))
struct linger {
	int l_onoff;
	int l_linger;
};
#define AF_UNSPEC 0
#define AF_UNIX 1
#define AF_LOCAL AF_UNIX
#define AF_INET 2
#define AF_INET6 10
#define AF_PACKET 17
#define PF_UNSPEC AF_UNSPEC
#define PF_UNIX AF_UNIX
#define PF_LOCAL AF_LOCAL
#define PF_INET AF_INET
#define PF_INET6 AF_INET6
#define PF_PACKET AF_PACKET
#define SOCK_STREAM 1
#define SOCK_DGRAM 2
#define SOCK_RAW 3
#define SOCK_SEQPACKET 5
#define SOCK_NONBLOCK 04000
#define SOCK_CLOEXEC 02000000
#define SOL_SOCKET 1
#define SO_DEBUG 1
#define SO_REUSEADDR 2
#define SO_TYPE 3
#define SO_ERROR 4
#define SO_DONTROUTE 5
#define SO_BROADCAST 6
#define SO_SNDBUF 7
#define SO_RCVBUF 8
#define SO_KEEPALIVE 9
#define SO_OOBINLINE 10
#define SO_LINGER 13
#define SO_REUSEPORT 15
#define SO_RCVLOWAT 18
#define SO_SNDLOWAT 19
#define SO_RCVTIMEO 20
#define SO_SNDTIMEO 21
#define SO_ACCEPTCONN 30
#define SO_PROTOCOL 38
#define SO_DOMAIN 39
#define SOMAXCONN 4096
#define MSG_OOB 0x1
#define MSG_PEEK 0x2
#define MSG_DONTROUTE 0x4
#define MSG_CTRUNC 0x8
#define MSG_TRUNC 0x20
#define MSG_DONTWAIT 0x40
#define MSG_EOR 0x80
#define MSG_WAITALL 0x100
#define MSG_NOSIGNAL 0x4000
#define MSG_CMSG_CLOEXEC 0x40000000
#define SHUT_RD 0
#define SHUT_WR 1
#define SHUT_RDWR 2
int socket(int domain, int type, int protocol);
int socketpair(int domain, int type, int protocol, int sv[2]);
int bind(int sockfd, const struct sockaddr *addr, socklen_t addrlen);
int listen(int sockfd, int backlog);
int accept(int sockfd, struct sockaddr *restrict addr, socklen_t *restrict addrlen);
int accept4(int sockfd, struct sockaddr *restrict addr, socklen_t *restrict addrlen, int flags);
int connect(int sockfd, const struct sockaddr *addr, socklen_t addrlen);
int shutdown(int sockfd, int how);
ssize_t send(int sockfd, const void *buf, size_t len, int flags);
ssize_t sendto(int sockfd, const void *buf, size_t len, int flags, const struct sockaddr *dest_addr, socklen_t addrlen);
ssize_t sendmsg(int sockfd, const struct msghdr *msg, int flags);
ssize_t recv(int sockfd, void *buf, size_t len, int flags);
ssize_t recvfrom(int sockfd, void *restrict buf, size_t len, int flags, struct sockaddr *restrict src_addr, socklen_t *restrict addrlen);
ssize_t recvmsg(int sockfd, struct msghdr *msg, int flags);
int getsockname(int sockfd, struct sockaddr *restrict addr, socklen_t *restrict addrlen);
int getpeername(int sockfd, struct sockaddr *restrict addr, socklen_t *restrict addrlen);
int getsockopt(int sockfd, int level, int optname, void *restrict optval, socklen_t *restrict optlen);
int setsockopt(int sockfd, int level, int optname, const void *optval, socklen_t optlen);
#endif
`,
	"netinet/in.h": `#ifndef __CC_NETINET_IN_H
#define __CC_NETINET_IN_H
#include <stdint.h>
#include <sys/socket.h>
typedef uint16_t in_port_t;
typedef uint32_t in_addr_t;
struct in_addr {
	in_addr_t s_addr;
};
struct in6_addr {
	union {
		uint8_t __s6_addr[16];
		uint16_t __s6_addr16[8];
		uint32_t __s6_addr32[4];
	} __in6_union;
};
#define s6_addr __in6_union.__s6_addr
#define s6_addr16 __in6_union.__s6_addr16
#define s6_addr32 __in6_union.__s6_addr32
struct sockaddr_in {
	sa_family_t sin_family;
	in_port_t sin_port;
	struct in_addr sin_addr;
	unsigned char sin_zero[8];
};
struct sockaddr_in6 {
	sa_family_t sin6_family;
	in_port_t sin6_port;
	uint32_t sin6_flowinfo;
	struct in6_addr sin6_addr;
	uint32_t sin6_scope_id;
};
struct ip_mreq {
	struct in_addr imr_multiaddr;
	struct in_addr imr_interface;
};
struct ipv6_mreq {
	struct in6_addr ipv6mr_multiaddr;
	unsigned ipv6mr_interface;
};
#define INADDR_ANY ((in_addr_t)0x00000000)
#define INADDR_BROADCAST ((in_addr_t)0xffffffff)
#define INADDR_NONE ((in_addr_t)0xffffffff)
#define INADDR_LOOPBACK ((in_addr_t)0x7f000001)
#define IN6ADDR_ANY_INIT {{{0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0}}}
#define IN6ADDR_LOOPBACK_INIT {{{0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1}}}
static const struct in6_addr in6addr_any = IN6ADDR_ANY_INIT;
static const struct in6_addr in6addr_loopback = IN6ADDR_LOOPBACK_INIT;
#define INET_ADDRSTRLEN 16
#define INET6_ADDRSTRLEN 46
#define IPPROTO_IP 0
#define IPPROTO_ICMP 1
#define IPPROTO_TCP 6
#define IPPROTO_UDP 17
#define IPPROTO_IPV6 41
#define IPPROTO_ICMPV6 58
#define IPPROTO_RAW 255
#define IP_TOS 1
#define IP_TTL 2
#define IP_MULTICAST_IF 32
#define IP_MULTICAST_TTL 33
#define IP_MULTICAST_LOOP 34
#define IP_ADD_MEMBERSHIP 35
#define IP_DROP_MEMBERSHIP 36
#define IPV6_UNICAST_HOPS 16
#define IPV6_MULTICAST_IF 17
#define IPV6_MULTICAST_HOPS 18
#define IPV6_MULTICAST_LOOP 19
#define IPV6_JOIN_GROUP 20
#define IPV6_LEAVE_GROUP 21
#define IPV6_V6ONLY 26
#define IN6_IS_ADDR_UNSPECIFIED(a) ((a)->s6_addr32[0] == 0 && (a)->s6_addr32[1] == 0 && (a)->s6_addr32[2] == 0 && (a)->s6_addr32[3] == 0)
#define IN6_IS_ADDR_LOOPBACK(a) ((a)->s6_addr32[0] == 0 && (a)->s6_addr32[1] == 0 && (a)->s6_addr32[2] == 0 && (a)->s6_addr[12] == 0 && (a)->s6_addr[13] == 0 && (a)->s6_addr[14] == 0 && (a)->s6_addr[15] == 1)
#define IN6_IS_ADDR_MULTICAST(a) ((a)->s6_addr[0] == 0xff)
#define IN6_IS_ADDR_LINKLOCAL(a) ((a)->s6_addr[0] == 0xfe && ((a)->s6_addr[1] & 0xc0) == 0x80)
#define IN6_IS_ADDR_V4MAPPED(a) ((a)->s6_addr32[0] == 0 && (a)->s6_addr32[1] == 0 && (a)->s6_addr[8] == 0 && (a)->s6_addr[9] == 0 && (a)->s6_addr[10] == 0xff && (a)->s6_addr[11] == 0xff)
#define IN6_ARE_ADDR_EQUAL(a, b) ((a)->s6_addr32[0] == (b)->s6_addr32[0] && (a)->s6_addr32[1] == (b)->s6_addr32[1] && (a)->s6_addr32[2] == (b)->s6_addr32[2] && (a)->s6_addr32[3] == (b)->s6_addr32[3])
uint16_t htons(uint16_t hostshort);
uint16_t ntohs(uint16_t netshort);
uint32_t htonl(uint32_t hostlong);
uint32_t ntohl(uint32_t netlong);
#endif
`,
	"netinet/tcp.h": `#ifndef __CC_NETINET_TCP_H
#define __CC_NETINET_TCP_H
#define TCP_NODELAY 1
#define TCP_MAXSEG 2
#define TCP_KEEPIDLE 4
#define TCP_KEEPINTVL 5
#define TCP_KEEPCNT 6
#endif
`,
	"arpa/inet.h": `#ifndef __CC_ARPA_INET_H
#define __CC_ARPA_INET_H
#include <netinet/in.h>
int inet_aton(const char *cp, struct in_addr *inp);
in_addr_t inet_addr(const char *cp);
char *__cc_inet_ntoa(in_addr_t in);
#define inet_ntoa(in) __cc_inet_ntoa((in).s_addr)
int inet_pton(int af, const char *restrict src, void *restrict dst);
const char *inet_ntop(int af, const void *restrict src, char *restrict dst, socklen_t size);
#endif
`,
	"netdb.h": `#ifndef __CC_NETDB_H
#define __CC_NETDB_H
#include <netinet/in.h>
struct addrinfo {
	int ai_flags;
	int ai_family;
	int ai_socktype;
	int ai_protocol;
	socklen_t ai_addrlen;
	struct sockaddr *ai_addr;
	char *ai_canonname;
	struct addrinfo *ai_next;
};
struct hostent {
	char *h_name;
	char **h_aliases;
	int h_addrtype;
	int h_length;
	char **h_addr_list;
};
#define h_addr h_addr_list[0]
struct servent {
	char *s_name;
	char **s_aliases;
	int s_port;
	char *s_proto;
};
struct protoent {
	char *p_name;
	char **p_aliases;
	int p_proto;
};
#define AI_PASSIVE 0x01
#define AI_CANONNAME 0x02
#define AI_NUMERICHOST 0x04
#define AI_V4MAPPED 0x08
#define AI_ALL 0x10
#define AI_ADDRCONFIG 0x20
#define AI_NUMERICSERV 0x400
#define NI_NUMERICHOST 1
#define NI_NUMERICSERV 2
#define NI_NOFQDN 4
#define NI_NAMEREQD 8
#define NI_DGRAM 16
#define NI_MAXHOST 1025
#define NI_MAXSERV 32
#define EAI_BADFLAGS -1
#define EAI_NONAME -2
#define EAI_AGAIN -3
#define EAI_FAIL -4
#define EAI_NODATA -5
#define EAI_FAMILY -6
#define EAI_SOCKTYPE -7
#define EAI_SERVICE -8
#define EAI_MEMORY -10
#define EAI_SYSTEM -11
#define EAI_OVERFLOW -12
#define HOST_NOT_FOUND 1
#define TRY_AGAIN 2
#define NO_RECOVERY 3
#define NO_DATA 4
#define NO_ADDRESS NO_DATA
int *__cc_h_errno(void);
#define h_errno (*__cc_h_errno())
int getaddrinfo(const char *restrict node, const char *restrict service, const struct addrinfo *restrict hints, struct addrinfo **restrict res);
void freeaddrinfo(struct addrinfo *res);
const char *gai_strerror(int errcode);
int getnameinfo(const struct sockaddr *restrict addr, socklen_t addrlen, char *restrict host, socklen_t hostlen, char *restrict serv, socklen_t servlen, int flags);
struct hostent *gethostbyname(const char *name);
struct hostent *gethostbyname2(const char *name, int af);
struct hostent *gethostbyaddr(const void *addr, socklen_t len, int type);
const char *hstrerror(int err);
struct servent *getservbyname(const char *name, const char *proto);
struct protoent *getprotobyname(const char *name);
struct protoent *getprotobynumber(int proto);
#endif
`,
	"sys/un.h": `#ifndef __CC_SYS_UN_H
#define __CC_SYS_UN_H
#include <sys/socket.h>
struct sockaddr_un {
	sa_family_t sun_family;
	char sun_path[108];
};
#endif
`,
	"net/if.h": `#ifndef __CC_NET_IF_H
#define __CC_NET_IF_H
#define IF_NAMESIZE 16
#define IFNAMSIZ IF_NAMESIZE
#define IFF_UP 0x1
#define IFF_BROADCAST 0x2
#define IFF_DEBUG 0x4
#define IFF_LOOPBACK 0x8
#define IFF_POINTOPOINT 0x10
#define IFF_NOTRAILERS 0x20
#define IFF_RUNNING 0x40
#define IFF_NOARP 0x80
#define IFF_PROMISC 0x100
#define IFF_ALLMULTI 0x200
#define IFF_MASTER 0x400
#define IFF_SLAVE 0x800
#define IFF_MULTICAST 0x1000
#define IFF_PORTSEL 0x2000
#define IFF_AUTOMEDIA 0x4000
#define IFF_DYNAMIC 0x8000
#define IFF_LOWER_UP 0x10000
#define IFF_DORMANT 0x20000
#define IFF_ECHO 0x40000
unsigned int if_nametoindex(const char *ifname);
char *if_indextoname(unsigned int ifindex, char *ifname);
#endif
`,
	"poll.h": `#ifndef __CC_POLL_H
#define __CC_POLL_H
struct pollfd {
	int fd;
	short events;
	short revents;
};
typedef unsigned long nfds_t;
#define POLLIN 0x001
#define POLLPRI 0x002
#define POLLOUT 0x004
#define POLLERR 0x008
#define POLLHUP 0x010
#define POLLNVAL 0x020
#define POLLRDNORM 0x040
#define POLLRDBAND 0x080
#define POLLWRNORM POLLOUT
#define POLLWRBAND 0x200
int poll(struct pollfd *fds, nfds_t nfds, int timeout);
#include <signal.h>
#include <time.h>
int ppoll(struct pollfd *fds, nfds_t nfds, const struct timespec *timeout, const sigset_t *sigmask);
#endif
`,
	"sys/poll.h": `#include <poll.h>
`,
	"sys/select.h": `#ifndef __CC_SYS_SELECT_H
#define __CC_SYS_SELECT_H
#include <sys/time.h>
#include <time.h>
#define FD_SETSIZE 1024
typedef struct {
	unsigned long fds_bits[FD_SETSIZE / (8 * sizeof(unsigned long))];
} fd_set;
#define __CC_NFDBITS (8 * (int)sizeof(unsigned long))
#define FD_ZERO(s) do { for (int __i = 0; __i < FD_SETSIZE / __CC_NFDBITS; __i++) (s)->fds_bits[__i] = 0; } while (0)
#define FD_SET(d, s) ((void)((s)->fds_bits[(d) / __CC_NFDBITS] |= (1UL << ((d) % __CC_NFDBITS))))
#define FD_CLR(d, s) ((void)((s)->fds_bits[(d) / __CC_NFDBITS] &= ~(1UL << ((d) % __CC_NFDBITS))))
#define FD_ISSET(d, s) (((s)->fds_bits[(d) / __CC_NFDBITS] & (1UL << ((d) % __CC_NFDBITS))) != 0)
int select(int nfds, fd_set *restrict readfds, fd_set *restrict writefds, fd_set *restrict exceptfds, struct timeval *restrict timeout);
int pselect(int nfds, fd_set *restrict readfds, fd_set *restrict writefds, fd_set *restrict exceptfds, const struct timespec *restrict timeout, const void *restrict sigmask);
#endif
`,
}
