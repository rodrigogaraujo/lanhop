#pragma once

#include <stdbool.h>
#include <stdint.h>

#define LANHOP_PROBE_DOWN 0
#define LANHOP_PROBE_DRAGGED 1
#define LANHOP_PROBE_UP 2

#define LANHOP_PROBE_NO_SESSION -1
#define LANHOP_PROBE_NO_FILE -2

#define LANHOP_PROBE_MARKER 0x6c68

#define LANHOP_PROBE_OBSERVE_NO_TAP -1
#define LANHOP_PROBE_OBSERVE_NOT_HELD -2
#define LANHOP_PROBE_OBSERVE_NO_REAL_UP 0
#define LANHOP_PROBE_OBSERVE_REAL_UP 1

char *lanhopProbeIdentity(void);
int64_t lanhopProbeDragChangeCount(void);
char *lanhopProbeDragDescription(void);
bool lanhopProbeLeftButtonHeld(bool includePosted);
void lanhopProbeCursor(double *x, double *y);
bool lanhopProbeCanPost(void);
bool lanhopProbeIsTrusted(void);
void lanhopProbeAskToPost(void);
void lanhopProbePostEscape(void);
void lanhopProbePostLeftMouse(int phase, double x, double y);
bool lanhopProbeReleasePendingDown(void);
int lanhopProbeDisplays(double *rects, int capacity);
bool lanhopProbeCanTapMouse(void);
bool lanhopProbeCanListen(void);
void lanhopProbeAskToListen(void);
int lanhopProbeCancelObserve(double gapSeconds, bool sessionTap, double observeSeconds);
int lanhopProbePromise(const char *path, double toX, double toY, bool manual, int steps, double holdSeconds, double timeoutSeconds);
