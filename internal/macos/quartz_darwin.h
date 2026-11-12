#pragma once

#include <stdbool.h>
#include <stdint.h>

#define LANHOP_MARKER 0x6c68

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
	bool autorepeat;
	int64_t marker;
} lanhopObservation;

typedef struct lanhopTap lanhopTap;

bool lanhopCanPost(void);
bool lanhopCanListen(void);
void lanhopPostKey(uint16_t keyCode, bool down, bool modifier, uint64_t flags);
void lanhopPostMouse(int action, double x, double y, int button, int64_t clicks, int64_t deltaX, int64_t deltaY, uint64_t flags);
void lanhopPostScroll(int32_t vertical, int32_t horizontal, uint64_t flags);
void lanhopCursor(double *x, double *y);
int lanhopDisplays(double *rects, int capacity);
lanhopTap *lanhopTapCreate(uintptr_t handle, bool active, bool last);
void lanhopFollowMouse(bool follow);
void lanhopTapRun(lanhopTap *tap);
void lanhopTapStop(lanhopTap *tap);
void lanhopTapFree(lanhopTap *tap);
