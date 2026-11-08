/* The native symbol fixture (testdata/build-native.sh): a tiny shared library, as an
   Android app's own .so would be: an exported function, a static one, an inlined helper
   (its lines are the line table's), and nodebug.c's function, built without debug info. */
static inline int clamp(int v) { return v < 0 ? 0 : v; }

__attribute__((noinline)) static int decode_frame(const unsigned char *p, int n) {
	int sum = 0;
	for (int i = 0; i < n; i++)
		sum += clamp(p[i] - 16);
	return sum;
}

int scale_nodebug(int n);

int mpv_render(const unsigned char *p, int n) {
	if (!p)
		return *(volatile int *)0;
	return decode_frame(p, n) + scale_nodebug(n);
}
