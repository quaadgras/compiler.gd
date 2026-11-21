// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

// POSIX's ndbm.h and devctl.h, and POSIX 2024's ppoll, sig2str and
// str2sig.

func init() {
	sysHeaders["ndbm.h"] = `#ifndef __CC_NDBM_H
#define __CC_NDBM_H
#include <stddef.h>
#include <sys/types.h>
typedef struct {
	void *dptr;
	size_t dsize;
} datum;
typedef struct __cc_dbm DBM;
#define DBM_INSERT 0
#define DBM_REPLACE 1
DBM *dbm_open(const char *file, int open_flags, mode_t file_mode);
void dbm_close(DBM *db);
int dbm_error(DBM *db);
int dbm_clearerr(DBM *db);
int __cc_dbm_store(DBM *db, const void *k, size_t kn, const void *v, size_t vn, int mode);
void __cc_dbm_fetch(DBM *db, const void *k, size_t kn, datum *out);
int __cc_dbm_delete(DBM *db, const void *k, size_t kn);
void __cc_dbm_firstkey(DBM *db, datum *out);
void __cc_dbm_nextkey(DBM *db, datum *out);
static inline int dbm_store(DBM *db, datum key, datum content, int store_mode) {
	return __cc_dbm_store(db, key.dptr, key.dsize, content.dptr, content.dsize, store_mode);
}
static inline datum dbm_fetch(DBM *db, datum key) {
	datum r;
	__cc_dbm_fetch(db, key.dptr, key.dsize, &r);
	return r;
}
static inline int dbm_delete(DBM *db, datum key) { return __cc_dbm_delete(db, key.dptr, key.dsize); }
static inline datum dbm_firstkey(DBM *db) {
	datum r;
	__cc_dbm_firstkey(db, &r);
	return r;
}
static inline datum dbm_nextkey(DBM *db) {
	datum r;
	__cc_dbm_nextkey(db, &r);
	return r;
}
#endif
`
	sysHeaders["devctl.h"] = `#ifndef __CC_DEVCTL_H
#define __CC_DEVCTL_H
#include <stddef.h>
int posix_devctl(int fildes, int dcmd, void *restrict dev_data_ptr, size_t nbyte, int *restrict dev_info_ptr);
#endif
`
	extendHeader("signal.h", `#define SIG2STR_MAX 32
int sig2str(int signum, char *str);
int str2sig(const char *restrict str, int *restrict pnum);
`)
	for c, g := range map[string]string{
		"dbm_open": "ndbm.DbmOpen", "dbm_close": "ndbm.DbmClose", "dbm_error": "ndbm.DbmError",
		"dbm_clearerr": "ndbm.DbmClearerr", "__cc_dbm_store": "ndbm.DbmStore", "__cc_dbm_fetch": "ndbm.DbmFetch",
		"__cc_dbm_delete": "ndbm.DbmDelete", "__cc_dbm_firstkey": "ndbm.DbmFirstkey",
		"__cc_dbm_nextkey": "ndbm.DbmNextkey",
		"posix_devctl":     "devctl.PosixDevctl",
		"ppoll":            "poll.Ppoll",
		"sig2str":          "signal.Sig2str", "str2sig": "signal.Str2sig",
	} {
		libc[c] = g
	}
}
