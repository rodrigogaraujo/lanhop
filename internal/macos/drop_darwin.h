#pragma once

#include <stdbool.h>
#include <stdint.h>

#define LANHOP_DROP_WINDOW_READY 0
#define LANHOP_DROP_WINDOW_NOT_SHOWN 1
#define LANHOP_DROP_WINDOW_COVERED 2

bool lanhopOnMainThread(void);
void lanhopAppRun(void);
void *lanhopDropOpen(uint64_t drop, const char *names, int64_t namesLength, const char *kinds, int64_t kindsLength, int count, double x, double y, double side);
int lanhopDropWindowState(void *drop);
void lanhopDropClose(void *drop);
