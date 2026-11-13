#pragma once

#include <stdint.h>

#define LANHOP_PASTEBOARD_TEXT 0
#define LANHOP_PASTEBOARD_NOT_TEXT 1
#define LANHOP_PASTEBOARD_PRIVATE 2
#define LANHOP_PASTEBOARD_TOO_LARGE 3
#define LANHOP_PASTEBOARD_REFUSED -1

int64_t lanhop_pasteboard_mark(const char *name);
int lanhop_pasteboard_read(const char *name, int64_t limit, char **text, int64_t *length);
int64_t lanhop_pasteboard_write(const char *name, const char *text, int64_t length, int markPrivate);
void lanhop_pasteboard_release(const char *name);
