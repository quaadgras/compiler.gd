// Sockets, name resolution, poll and select (internal/c23/sock and
// internal/c23's special.go). TestSock diffs the output with gcc's (and
// musl's), so it prints nothing that differs between runs: no ports, no
// pids, no resolver messages.

#include <arpa/inet.h>
#include <errno.h>
#include <fcntl.h>
#include <netdb.h>
#include <netinet/in.h>
#include <netinet/tcp.h>
#include <poll.h>
#include <stdio.h>
#include <string.h>
#include <sys/select.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/un.h>
#include <unistd.h>

static void check(const char *what, int ok) { printf("%s: %s\n", what, ok ? "ok" : "FAIL"); }

static int listener(struct sockaddr_in *addr) {
	int ls = socket(AF_INET, SOCK_STREAM, 0);
	int one = 1;
	setsockopt(ls, SOL_SOCKET, SO_REUSEADDR, &one, sizeof one);
	memset(addr, 0, sizeof *addr);
	addr->sin_family = AF_INET;
	addr->sin_addr.s_addr = htonl(INADDR_LOOPBACK);
	addr->sin_port = 0;
	if (bind(ls, (struct sockaddr *)addr, sizeof *addr) < 0)
		perror("bind");
	socklen_t n = sizeof *addr;
	getsockname(ls, (struct sockaddr *)addr, &n);
	listen(ls, 8);
	return ls;
}

static void tcp(void) {
	struct sockaddr_in addr;
	int ls = listener(&addr);
	check("bound port", ntohs(addr.sin_port) != 0);
	int v;
	socklen_t vl = sizeof v;
	getsockopt(ls, SOL_SOCKET, SO_ACCEPTCONN, &v, &vl);
	int ac = v;
	getsockopt(ls, SOL_SOCKET, SO_TYPE, &v, &vl);
	printf("acceptconn %d, type %d\n", ac, v);

	fcntl(ls, F_SETFL, fcntl(ls, F_GETFL) | O_NONBLOCK);
	int r = accept(ls, NULL, NULL);
	printf("accept nonblock %d %s\n", r, errno == EAGAIN ? "EAGAIN" : "?");

	int c = socket(AF_INET, SOCK_STREAM, 0);
	printf("connect %d\n", connect(c, (struct sockaddr *)&addr, sizeof addr));
	struct pollfd pfd = {ls, POLLIN, 0};
	r = poll(&pfd, 1, 5000);
	printf("poll listener %d revents %d\n", r, pfd.revents);
	struct sockaddr_in peer;
	socklen_t pl = sizeof peer;
	int s = accept(ls, (struct sockaddr *)&peer, &pl);
	char buf[64];
	inet_ntop(AF_INET, &peer.sin_addr, buf, sizeof buf);
	printf("accepted %d, family %d, addr %s, len %u\n", s >= 0, peer.sin_family, buf, (unsigned)pl);

	int one = 1;
	r = setsockopt(c, IPPROTO_TCP, TCP_NODELAY, &one, sizeof one);
	printf("nodelay %d", r);
	vl = sizeof v;
	getsockopt(c, IPPROTO_TCP, TCP_NODELAY, &v, &vl);
	printf(" %d\n", v != 0);

	printf("send %zd\n", send(c, "hello", 5, 0));
	pfd = (struct pollfd){s, POLLIN, 0};
	poll(&pfd, 1, 5000);
	int in = (pfd.revents & POLLIN) != 0;
	pfd = (struct pollfd){s, POLLIN | POLLOUT, 0};
	poll(&pfd, 1, 5000);
	printf("poll conn in %d out %d\n", in && (pfd.revents & POLLIN) != 0, (pfd.revents & POLLOUT) != 0);
	ssize_t k = recv(s, buf, 3, MSG_PEEK);
	printf("peek %zd %.*s\n", k, (int)k, buf);
	k = recv(s, buf, sizeof buf, MSG_WAITALL & 0);
	printf("recv %zd %.*s\n", k, (int)k, buf);
	fcntl(s, F_SETFL, O_NONBLOCK);
	k = recv(s, buf, sizeof buf, 0);
	printf("recv nonblock %zd %s\n", k, errno == EAGAIN ? "EAGAIN" : "?");
	k = recv(s, buf, sizeof buf, MSG_DONTWAIT);
	printf("recv dontwait %zd %s\n", k, errno == EAGAIN ? "EAGAIN" : "?");
	fcntl(s, F_SETFL, 0);

	// read and write work on sockets, and stdio over fdopen.
	write(s, "world", 5);
	k = read(c, buf, sizeof buf);
	printf("read %zd %.*s\n", k, (int)k, buf);
	FILE *f = fdopen(dup(s), "w");
	fprintf(f, "via %s\n", "stdio");
	fclose(f);
	k = 0;
	while (k < 10) {
		ssize_t n = recv(c, buf + k, sizeof buf - k, 0);
		if (n <= 0)
			break;
		k += n;
	}
	printf("fdopen %zd %.*s", k, (int)k, buf);

	struct sockaddr_in pa;
	pl = sizeof pa;
	getpeername(c, (struct sockaddr *)&pa, &pl);
	printf("peer port matches %d\n", pa.sin_port == addr.sin_port);
	struct stat st;
	fstat(c, &st);
	long off = lseek(c, 0, SEEK_CUR);
	printf("S_ISSOCK %d, lseek %ld %s\n", S_ISSOCK(st.st_mode), off, errno == ESPIPE ? "ESPIPE" : "?");

	shutdown(c, SHUT_WR);
	k = recv(s, buf, sizeof buf, 0);
	printf("after shutdown recv %zd\n", k);
	fd_set rs;
	FD_ZERO(&rs);
	FD_SET(s, &rs);
	struct timeval tv = {5, 0};
	r = select(s + 1, &rs, NULL, NULL, &tv);
	printf("select eof %d isset %d\n", r, FD_ISSET(s, &rs));
	close(s);
	close(c);

	// Refused: the listener's port, once it is closed.
	close(ls);
	c = socket(AF_INET, SOCK_STREAM, 0);
	r = connect(c, (struct sockaddr *)&addr, sizeof addr);
	printf("refused %d %s\n", r, errno == ECONNREFUSED ? "ECONNREFUSED" : strerror(errno));
	close(c);
	int p[2];
	pipe(p);
	r = send(p[1], "x", 1, 0);
	printf("not a socket %d %s\n", r, errno == ENOTSOCK ? "ENOTSOCK" : "?");
	close(p[0]);
	close(p[1]);
	r = send(999, "x", 1, 0);
	printf("bad fd %d %s\n", r, errno == EBADF ? "EBADF" : "?");
}

static void nonblocking_connect(void) {
	struct sockaddr_in addr;
	int ls = listener(&addr);
	int c = socket(AF_INET, SOCK_STREAM | SOCK_NONBLOCK, 0);
	int r = connect(c, (struct sockaddr *)&addr, sizeof addr);
	printf("nonblocking connect %s\n", r == 0 || errno == EINPROGRESS ? "ok" : strerror(errno));
	struct pollfd pfd = {c, POLLOUT, 0};
	r = poll(&pfd, 1, 5000);
	printf("poll out %d %d\n", r, pfd.revents & POLLOUT);
	int err = -1;
	socklen_t el = sizeof err;
	getsockopt(c, SOL_SOCKET, SO_ERROR, &err, &el);
	printf("so_error %d\n", err);
	int s = accept(ls, NULL, NULL);
	printf("accept %d\n", s >= 0);
	close(s);
	close(c);
	close(ls);
}

static void udp(void) {
	int a = socket(AF_INET, SOCK_DGRAM, 0), b = socket(AF_INET, SOCK_DGRAM, 0);
	struct sockaddr_in aa = {.sin_family = AF_INET, .sin_addr.s_addr = htonl(INADDR_LOOPBACK)};
	bind(a, (struct sockaddr *)&aa, sizeof aa);
	socklen_t n = sizeof aa;
	getsockname(a, (struct sockaddr *)&aa, &n);
	ssize_t sent = sendto(b, "ping", 4, 0, (struct sockaddr *)&aa, sizeof aa);
	printf("udp sendto %zd\n", sent);
	char buf[16];
	struct sockaddr_in from;
	socklen_t fl = sizeof from;
	ssize_t k = recvfrom(a, buf, sizeof buf, 0, (struct sockaddr *)&from, &fl);
	struct sockaddr_in bb;
	n = sizeof bb;
	getsockname(b, (struct sockaddr *)&bb, &n);
	printf("udp recvfrom %zd %.*s from b %d\n", k, (int)k, buf, from.sin_port == bb.sin_port);
	connect(b, (struct sockaddr *)&aa, sizeof aa);
	send(b, "pong", 4, 0);
	struct iovec iov[2] = {{buf, 2}, {buf + 8, 8}};
	struct msghdr msg = {.msg_name = &from, .msg_namelen = sizeof from, .msg_iov = iov, .msg_iovlen = 2};
	k = recvmsg(a, &msg, 0);
	printf("udp recvmsg %zd %.2s %.2s namelen %u\n", k, buf, buf + 8, (unsigned)msg.msg_namelen);
	close(a);
	close(b);
}

static void pair(void) {
	int sv[2];
	int r = socketpair(AF_UNIX, SOCK_STREAM, 0, sv);
	printf("socketpair %d\n", r);
	write(sv[0], "abc", 3);
	char buf[8];
	ssize_t k = read(sv[1], buf, sizeof buf);
	printf("pair read %zd %.*s\n", k, (int)k, buf);
	write(sv[1], "xy", 2);
	close(sv[1]);
	k = read(sv[0], buf, sizeof buf);
	ssize_t k2 = read(sv[0], buf + 2, sizeof buf - 2);
	printf("pair %zd %.2s then %zd\n", k, buf, k2);
	close(sv[0]);
}

static void unix_stream(void) {
	unlink("sock.test");
	int ls = socket(AF_UNIX, SOCK_STREAM, 0);
	struct sockaddr_un ua = {.sun_family = AF_UNIX};
	strcpy(ua.sun_path, "sock.test");
	int rb = bind(ls, (struct sockaddr *)&ua, sizeof ua);
	int rl = listen(ls, 1);
	printf("unix bind %d listen %d\n", rb, rl);
	int c = socket(AF_UNIX, SOCK_STREAM, 0);
	int rc = connect(c, (struct sockaddr *)&ua, sizeof ua);
	printf("unix connect %d\n", rc);
	int s = accept(ls, NULL, NULL);
	send(c, "unix", 4, 0);
	char buf[8];
	ssize_t k = recv(s, buf, sizeof buf, 0);
	printf("unix recv %zd %.*s\n", k, (int)k, buf);
	struct sockaddr_un got;
	socklen_t gl = sizeof got;
	getsockname(ls, (struct sockaddr *)&got, &gl);
	printf("unix name %s\n", got.sun_path);
	close(s);
	close(c);
	close(ls);
	printf("unix file stays %d\n", access("sock.test", F_OK) == 0);
	unlink("sock.test");
}

static void resolve(void) {
	struct addrinfo hints = {.ai_family = AF_INET, .ai_socktype = SOCK_STREAM}, *res;
	int r = getaddrinfo("127.0.0.1", "80", &hints, &res);
	char host[64], serv[16];
	printf("getaddrinfo numeric %d", r);
	if (r == 0) {
		struct sockaddr_in *sin = (struct sockaddr_in *)res->ai_addr;
		printf(" family %d type %d proto %d len %u port %d next %d", res->ai_family, res->ai_socktype,
			res->ai_protocol, (unsigned)res->ai_addrlen, ntohs(sin->sin_port), res->ai_next != NULL);
		getnameinfo(res->ai_addr, res->ai_addrlen, host, sizeof host, serv, sizeof serv, NI_NUMERICHOST | NI_NUMERICSERV);
		printf(" name %s %s", host, serv);
		freeaddrinfo(res);
	}
	printf("\n");
	hints = (struct addrinfo){.ai_family = AF_INET6, .ai_flags = AI_PASSIVE | AI_NUMERICSERV, .ai_socktype = SOCK_DGRAM};
	r = getaddrinfo(NULL, "5353", &hints, &res);
	printf("passive %d", r);
	if (r == 0) {
		struct sockaddr_in6 *s6 = (struct sockaddr_in6 *)res->ai_addr;
		printf(" family %d any %d port %d", res->ai_family, IN6_IS_ADDR_UNSPECIFIED(&s6->sin6_addr), ntohs(s6->sin6_port));
		freeaddrinfo(res);
	}
	printf("\n");
	hints = (struct addrinfo){.ai_family = AF_UNSPEC, .ai_flags = AI_NUMERICHOST};
	r = getaddrinfo("example.com", NULL, &hints, &res);
	printf("numerichost name %d\n", r == EAI_NONAME);
	hints = (struct addrinfo){.ai_family = AF_INET, .ai_socktype = SOCK_STREAM};
	r = getaddrinfo("localhost", "7", &hints, &res);
	printf("localhost %d", r);
	if (r == 0) {
		printf(" %s", inet_ntoa(((struct sockaddr_in *)res->ai_addr)->sin_addr));
		freeaddrinfo(res);
	}
	int bf = getaddrinfo("x", NULL, &(struct addrinfo){.ai_flags = 0x1000}, &res);
	int fam = getaddrinfo("x", NULL, &(struct addrinfo){.ai_family = 99}, &res);
	printf("\nbad flags %d, family %d\n", bf, fam);
}

static void addresses(void) {
	struct in_addr a;
	int r = inet_aton("127.1", &a);
	printf("aton %d %s", r, inet_ntoa(a));
	r = inet_aton("0x7f.0.0.2", &a);
	printf(" %d %s", r, inet_ntoa(a));
	printf(" %d\n", inet_aton("1.2.3.256", &a));
	printf("inet_addr %08x %d\n", ntohl(inet_addr("10.0.0.1")), inet_addr("nope") == INADDR_NONE);
	unsigned char b6[16];
	char s[INET6_ADDRSTRLEN];
	r = inet_pton(AF_INET6, "2001:db8::1", b6);
	printf("pton6 %d %s\n", r, inet_ntop(AF_INET6, b6, s, sizeof s));
	r = inet_pton(AF_INET6, "::ffff:1.2.3.4", b6);
	printf("pton6 mapped %d %s\n", r, inet_ntop(AF_INET6, b6, s, sizeof s));
	r = inet_pton(AF_INET, "1.2.3.4", b6);
	printf("pton4 %d %d\n", r, inet_pton(AF_INET, "1.2.3", b6));
	const char *np = inet_ntop(AF_INET, b6, s, 4);
	printf("ntop small %d %s\n", np == NULL, errno == ENOSPC ? "ENOSPC" : "?");
	printf("hton %04x %08x\n", htons(0x1234), htonl(0x01020304));
	printf("sizes %zu %zu %zu %zu\n", sizeof(struct sockaddr_in), sizeof(struct sockaddr_in6),
		sizeof(struct sockaddr_storage), sizeof(struct sockaddr_un));
}

static void pipes(void) {
	int p[2];
	pipe(p);
	struct pollfd pfd[3] = {{p[0], POLLIN, 0}, {p[1], POLLOUT, 0}, {-1, POLLIN, 0}};
	int r = poll(pfd, 3, 0);
	printf("poll pipe empty %d %d %d %d\n", r, pfd[0].revents, pfd[1].revents, pfd[2].revents);
	write(p[1], "z", 1);
	pfd[0].revents = 0;
	r = poll(pfd, 1, 5000);
	printf("poll pipe %d %d\n", r, pfd[0].revents);
	char c = 0;
	ssize_t k = read(p[0], &c, 1);
	printf("read %zd %c\n", k, c);
	fcntl(p[0], F_SETFL, O_NONBLOCK);
	k = read(p[0], &c, 1);
	printf("read nonblock %zd %s\n", k, errno == EAGAIN ? "EAGAIN" : "?");
	close(p[1]);
	pfd[0].revents = 0;
	poll(pfd, 1, 5000);
	k = read(p[0], &c, 1);
	printf("poll closed in %d hup %d, read %zd\n", (pfd[0].revents & POLLIN) != 0, (pfd[0].revents & POLLHUP) != 0, k);
	struct pollfd bad = {999, POLLIN, 0};
	r = poll(&bad, 1, 0);
	printf("poll bad %d %d\n", r, bad.revents);
	int q[2];
	pipe(q);
	fd_set ws;
	FD_ZERO(&ws);
	FD_SET(q[1], &ws);
	struct timeval tv = {0, 0};
	r = select(q[1] + 1, NULL, &ws, NULL, &tv);
	printf("select pipe writable %d %d\n", r, FD_ISSET(q[1], &ws));
	close(q[0]);
	close(q[1]);
	close(p[0]);
	FD_ZERO(&ws);
	FD_SET(p[0], &ws);
	r = select(p[0] + 1, &ws, NULL, NULL, &tv);
	printf("select closed %d %s\n", r, errno == EBADF ? "EBADF" : "?");
}

static void unix_dgram(void) {
	unlink("dgram.srv");
	unlink("dgram.cli");
	int srv = socket(AF_UNIX, SOCK_DGRAM, 0);
	struct sockaddr_un sa = {.sun_family = AF_UNIX};
	strcpy(sa.sun_path, "dgram.srv");
	int r = bind(srv, (struct sockaddr *)&sa, sizeof sa);
	printf("dgram bind %d\n", r);
	int cli = socket(AF_UNIX, SOCK_DGRAM, 0);
	struct sockaddr_un ca = {.sun_family = AF_UNIX};
	strcpy(ca.sun_path, "dgram.cli");
	bind(cli, (struct sockaddr *)&ca, sizeof ca);
	ssize_t k = sendto(cli, "one", 3, 0, (struct sockaddr *)&sa, sizeof sa);
	sendto(cli, "two!", 4, 0, (struct sockaddr *)&sa, sizeof sa);
	char buf[16];
	struct sockaddr_un from;
	socklen_t fl = sizeof from;
	k = recvfrom(srv, buf, sizeof buf, 0, (struct sockaddr *)&from, &fl);
	printf("dgram recv %zd %.*s from %s\n", k, (int)k, buf, from.sun_path);
	k = recv(srv, buf, 2, 0);
	printf("dgram truncated %zd %.2s\n", k, buf);
	sendto(srv, "back", 4, 0, (struct sockaddr *)&from, fl);
	k = recv(cli, buf, sizeof buf, 0);
	printf("dgram reply %zd %.*s\n", k, (int)k, buf);
	int un = socket(AF_UNIX, SOCK_DGRAM, 0);
	r = connect(un, (struct sockaddr *)&sa, sizeof sa);
	k = send(un, "anon", 4, 0);
	printf("dgram connected %d %zd\n", r, k);
	k = recv(srv, buf, sizeof buf, 0);
	printf("dgram anon %zd %.*s\n", k, (int)k, buf);
	close(un);
	close(cli);
	close(srv);
	unlink("dgram.srv");
	unlink("dgram.cli");

	int sv[2];
	r = socketpair(AF_UNIX, SOCK_DGRAM, 0, sv);
	send(sv[0], "abc", 3, 0);
	send(sv[0], "de", 2, 0);
	k = recv(sv[1], buf, sizeof buf, 0);
	ssize_t k2 = recv(sv[1], buf + 8, sizeof buf - 8, 0);
	printf("dgram pair %d %zd %zd\n", r, k, k2);
	close(sv[0]);
	close(sv[1]);
}

// sendfd sends fd over s with SCM_RIGHTS, and a byte.
static ssize_t sendfd(int s, int fd) {
	char c = 'x';
	struct iovec iov = {&c, 1};
	union {
		struct cmsghdr h;
		char buf[CMSG_SPACE(sizeof(int))];
	} u;
	memset(&u, 0, sizeof u);
	struct msghdr m = {.msg_iov = &iov, .msg_iovlen = 1, .msg_control = u.buf, .msg_controllen = sizeof u.buf};
	struct cmsghdr *h = CMSG_FIRSTHDR(&m);
	h->cmsg_level = SOL_SOCKET;
	h->cmsg_type = SCM_RIGHTS;
	h->cmsg_len = CMSG_LEN(sizeof(int));
	memcpy(CMSG_DATA(h), &fd, sizeof fd);
	return sendmsg(s, &m, 0);
}

// recvfd receives a descriptor sent with a byte, or -1; room is the
// control buffer's size.
static int recvfd(int s, size_t room, int *flags) {
	char c;
	struct iovec iov = {&c, 1};
	union {
		struct cmsghdr h;
		char buf[64];
	} u;
	struct msghdr m = {.msg_iov = &iov, .msg_iovlen = 1, .msg_control = u.buf, .msg_controllen = room};
	if (recvmsg(s, &m, 0) != 1)
		return -2;
	*flags = m.msg_flags;
	struct cmsghdr *h = CMSG_FIRSTHDR(&m);
	if (h == NULL || h->cmsg_type != SCM_RIGHTS)
		return -1;
	int fd;
	memcpy(&fd, CMSG_DATA(h), sizeof fd);
	return fd;
}

static void rights(void) {
	for (int type = SOCK_STREAM; type <= SOCK_DGRAM; type++) {
		int sv[2], p[2];
		socketpair(AF_UNIX, type, 0, sv);
		pipe(p);
		ssize_t k = sendfd(sv[0], p[1]);
		close(p[1]);
		int flags = 0;
		int fd = recvfd(sv[1], 64, &flags);
		printf("rights %s sent %zd got %d flags %d", type == SOCK_STREAM ? "stream" : "dgram", k, fd >= 0, flags);
		write(fd, "via fd", 6);
		close(fd);
		char buf[16];
		k = read(p[0], buf, sizeof buf);
		printf(", pipe read %zd %.*s", k, (int)k, buf);
		k = read(p[0], buf, sizeof buf);
		printf(", then %zd\n", k);
		close(p[0]);

		// No room for the descriptor: it is closed, MSG_CTRUNC.
		pipe(p);
		sendfd(sv[0], p[1]);
		close(p[1]);
		fd = recvfd(sv[1], 0, &flags);
		k = read(p[0], buf, sizeof buf);
		printf("rights truncated %d ctrunc %d, writer gone %zd\n", fd, (flags & MSG_CTRUNC) != 0, k);
		close(p[0]);
		close(sv[0]);
		close(sv[1]);
	}
}

int main(void) {
	setvbuf(stdout, NULL, _IONBF, 0);
	tcp();
	nonblocking_connect();
	udp();
	pair();
	unix_stream();
	resolve();
	addresses();
	pipes();
	unix_dgram();
	rights();
	return 0;
}
