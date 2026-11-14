#pragma once

#include <stdbool.h>
#include <stdint.h>

#define LANHOP_DROP_WINDOW_READY 0
#define LANHOP_DROP_WINDOW_NOT_SHOWN 1
#define LANHOP_DROP_WINDOW_COVERED 2
#define LANHOP_DROP_WINDOW_MISSES_THE_CURSOR 3

#define LANHOP_DROP_MISSES_THE_MOUSE 0
#define LANHOP_DROP_TAKES_THE_MOUSE 1
#define LANHOP_DROP_MAIN_THREAD_LATE -1

bool lanhopOnMainThread(void);
bool lanhopHasGUISession(void);
void lanhopAppRun(void);
void lanhopAppQuit(void);
void lanhopExitOnMainThread(int code);
void *lanhopDropOpen(uint64_t drop, const char *names, int64_t namesLength, const char *kinds, int64_t kindsLength, int count, double x, double y, double side);
int lanhopDropWindowState(void *drop);
bool lanhopDropRefuseDrops(void *drop);
int lanhopDropTakeTheMouseAt(void *drop, double x, double y);
bool lanhopDropClose(void *drop);
