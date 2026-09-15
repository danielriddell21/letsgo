#include "checksum.h"

// Enough real C that the compiler has something to be nondeterministic about:
// a loop, a branch and a table, rather than a one-line function the optimiser
// folds away before any of that matters.
static const unsigned long seed[4] = {5381UL, 33UL, 65599UL, 131UL};

unsigned long checksum(const char *s) {
	unsigned long h = seed[0];
	for (int i = 0; s[i] != '\0'; i++) {
		unsigned char c = (unsigned char)s[i];
		h = h * seed[1 + (i % 3)] + c;
		if (c & 1) {
			h ^= h >> 7;
		}
	}
	return h;
}
