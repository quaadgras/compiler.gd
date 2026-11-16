// fs_emscripten.c is the Emscripten program of fs_test.mjs and memfs.mjs:
// one that writes a file, and whose FS the page (or node) then uses.
//
//	emcc fs_emscripten.c -o fs_emscripten.mjs -sMODULARIZE -sEXPORT_ES6 \
//		-sENVIRONMENT=web,node,worker -sEXPORTED_RUNTIME_METHODS=FS,IDBFS -lidbfs.js
#include <stdio.h>

int main() {
	FILE *f = fopen("/tmp/from_c.txt", "w");
	fputs("written by C\n", f);
	fclose(f);
	return 0;
}
