#pragma once

#include <stdbool.h>
#include <stdint.h>

#define LANHOP_PROBE_DOWN 0
#define LANHOP_PROBE_DRAGGED 1
#define LANHOP_PROBE_UP 2

#define LANHOP_PROBE_NO_SESSION -1
#define LANHOP_PROBE_NO_FILE -2

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
int lanhopProbePromise(const char *path, double toX, double toY, bool manual, int steps, double holdSeconds, double timeoutSeconds);
