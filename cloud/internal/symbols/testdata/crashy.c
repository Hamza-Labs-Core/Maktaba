#include <stdio.h>
static int depth(int n) { if (n <= 0) { int *p = 0; return *p; } return depth(n - 1) + 1; }
int play_item(int n) { return depth(n) * 2; }
int main(int argc, char **argv) { (void)argv; printf("%d\n", play_item(argc)); return 0; }
