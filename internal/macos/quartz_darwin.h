#pragma once

#include <stdbool.h>
#include <stdint.h>

#define LANHOP_MARKER 0x6c68

#define LANHOP_TYPE_LEFT_MOUSE_DOWN 1
#define LANHOP_TYPE_LEFT_MOUSE_UP 2
#define LANHOP_TYPE_RIGHT_MOUSE_DOWN 3
#define LANHOP_TYPE_RIGHT_MOUSE_UP 4
#define LANHOP_TYPE_MOUSE_MOVED 5
#define LANHOP_TYPE_LEFT_MOUSE_DRAGGED 6
#define LANHOP_TYPE_RIGHT_MOUSE_DRAGGED 7
#define LANHOP_TYPE_KEY_DOWN 10
#define LANHOP_TYPE_KEY_UP 11
#define LANHOP_TYPE_FLAGS_CHANGED 12
#define LANHOP_TYPE_SCROLL_WHEEL 22
#define LANHOP_TYPE_OTHER_MOUSE_DOWN 25
#define LANHOP_TYPE_OTHER_MOUSE_UP 26
#define LANHOP_TYPE_OTHER_MOUSE_DRAGGED 27
#define LANHOP_TYPE_TAP_DISABLED 0xFFFFFFFE

enum { lanhopActionMoved, lanhopActionDragged, lanhopActionDown, lanhopActionUp };

typedef struct {
	uint32_t type;
	uint16_t keyCode;
	uint64_t flags;
	double x;
	double y;
	int64_t deltaX;
	int64_t deltaY;
	int64_t clicks;
	int64_t button;
	int64_t scrollVertical;
	int64_t scrollHorizontal;
	double pointsVertical;
	double pointsHorizontal;
	bool continuous;
	int64_t marker;
	bool autorepeat;
} lanhopObservation;

typedef struct lanhopTap lanhopTap;

bool lanhopCanPost(void);
bool lanhopCanListen(void);
bool lanhopIsTrusted(void);
void lanhopPostKey(uint16_t keyCode, bool down, bool modifier, bool autorepeat, uint64_t flags);
void lanhopPostMouse(int action, double x, double y, int button, int64_t clicks, int64_t deltaX, int64_t deltaY, uint64_t flags);
void lanhopPostScroll(int32_t vertical, int32_t horizontal, uint64_t flags);
void lanhopCursor(double *x, double *y);
void lanhopPlaceCursor(double x, double y);
void lanhopPutCursorBack(double x, double y);
void lanhopHideCursor(void);
void lanhopShowCursor(void);
int lanhopDisplays(double *rects, int capacity);
bool lanhopKeyRepeat(int64_t *delayNanoseconds, int64_t *intervalNanoseconds);
lanhopTap *lanhopTapCreate(uintptr_t handle, bool active, bool last);
bool lanhopKeyIsDown(uint16_t keyCode);
bool lanhopButtonIsDown(int button);
void lanhopTapRun(lanhopTap *tap);
void lanhopTapStop(lanhopTap *tap);
void lanhopTapFree(lanhopTap *tap);
