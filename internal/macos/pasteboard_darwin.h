#pragma once

#include <stdint.h>

#define LANHOP_PASTEBOARD_TEXT 0
#define LANHOP_PASTEBOARD_NOT_TEXT 1
#define LANHOP_PASTEBOARD_PRIVATE 2
#define LANHOP_PASTEBOARD_TOO_LARGE 3
#define LANHOP_PASTEBOARD_EMPTY 4
#define LANHOP_PASTEBOARD_FILES 5
#define LANHOP_PASTEBOARD_NOT_FILES 6
#define LANHOP_PASTEBOARD_TOO_MANY 7
#define LANHOP_PASTEBOARD_NO_MEMORY 8
#define LANHOP_PASTEBOARD_REFUSED -1

int64_t lanhop_pasteboard_mark(const char *name);
int lanhop_pasteboard_read(const char *name, int64_t limit, char **text, int64_t *length);
int64_t lanhop_pasteboard_write(const char *name, const char *text, int64_t length, int markPrivate);
int lanhop_pasteboard_read_files(const char *name, int64_t limit, char **paths, int64_t *length, int64_t *count);
int64_t lanhop_pasteboard_write_files(const char *name, const char *paths, int64_t length, int markPrivate);
int64_t lanhop_pasteboard_write_typed(const char *name, const char *type, const char *data, int64_t dataLength, const char *text, int64_t length);
int lanhop_pasteboard_types(const char *name, char **types, int64_t *length);
char *lanhop_file_reference_url(const char *path);
void lanhop_pasteboard_empty(const char *name);
void lanhop_pasteboard_release(const char *name);
