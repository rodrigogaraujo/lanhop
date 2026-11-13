#include "pasteboard_darwin.h"

#import <AppKit/AppKit.h>
#include <stdlib.h>
#include <string.h>

static NSString *const lanhopConcealedType = @"org.nspasteboard.ConcealedType";

static NSPasteboard *lanhopBoard(const char *name) {
	if (name == NULL) {
		return [NSPasteboard generalPasteboard];
	}
	return [NSPasteboard pasteboardWithName:[NSString stringWithUTF8String:name]];
}

int64_t lanhop_pasteboard_mark(const char *name) {
	@autoreleasepool {
		return (int64_t)[lanhopBoard(name) changeCount];
	}
}

int lanhop_pasteboard_read(const char *name, int64_t limit, char **text, int64_t *length) {
	@autoreleasepool {
		NSPasteboard *board = lanhopBoard(name);
		if ([[board pasteboardItems] count] == 0) {
			return LANHOP_PASTEBOARD_EMPTY;
		}
		if ([[board types] containsObject:lanhopConcealedType]) {
			return LANHOP_PASTEBOARD_PRIVATE;
		}
		NSString *string = [board stringForType:NSPasteboardTypeString];
		if (string == nil) {
			return LANHOP_PASTEBOARD_NOT_TEXT;
		}
		if ((int64_t)[string length] > limit) {
			return LANHOP_PASTEBOARD_TOO_LARGE;
		}
		NSData *data = [string dataUsingEncoding:NSUTF8StringEncoding];
		if (data == nil) {
			return LANHOP_PASTEBOARD_NOT_TEXT;
		}
		if ((int64_t)[data length] > limit) {
			return LANHOP_PASTEBOARD_TOO_LARGE;
		}
		*text = malloc([data length] + 1);
		if (*text == NULL) {
			return LANHOP_PASTEBOARD_TOO_LARGE;
		}
		memcpy(*text, [data bytes], [data length]);
		*length = (int64_t)[data length];
		return LANHOP_PASTEBOARD_TEXT;
	}
}

int64_t lanhop_pasteboard_write(const char *name, const char *text, int64_t length, int markPrivate) {
	@autoreleasepool {
		NSString *string = [[[NSString alloc] initWithBytes:text length:(NSUInteger)length encoding:NSUTF8StringEncoding] autorelease];
		if (string == nil) {
			return LANHOP_PASTEBOARD_REFUSED;
		}
		NSPasteboard *board = lanhopBoard(name);
		[board clearContents];
		if (![board setString:string forType:NSPasteboardTypeString]) {
			return LANHOP_PASTEBOARD_REFUSED;
		}
		if (markPrivate && ![board setString:@"" forType:lanhopConcealedType]) {
			return LANHOP_PASTEBOARD_REFUSED;
		}
		return (int64_t)[board changeCount];
	}
}

int64_t lanhop_pasteboard_write_typed(const char *name, const char *type, const char *data, int64_t dataLength, const char *text, int64_t length) {
	@autoreleasepool {
		NSPasteboard *board = lanhopBoard(name);
		[board clearContents];
		NSData *bytes = [NSData dataWithBytes:data length:(NSUInteger)dataLength];
		if (![board setData:bytes forType:[NSString stringWithUTF8String:type]]) {
			return LANHOP_PASTEBOARD_REFUSED;
		}
		if (text == NULL) {
			return (int64_t)[board changeCount];
		}
		NSString *plain = [[[NSString alloc] initWithBytes:text length:(NSUInteger)length encoding:NSUTF8StringEncoding] autorelease];
		if (plain == nil || ![board setString:plain forType:NSPasteboardTypeString]) {
			return LANHOP_PASTEBOARD_REFUSED;
		}
		return (int64_t)[board changeCount];
	}
}

void lanhop_pasteboard_empty(const char *name) {
	@autoreleasepool {
		[lanhopBoard(name) clearContents];
	}
}

void lanhop_pasteboard_release(const char *name) {
	if (name == NULL) {
		return;
	}
	@autoreleasepool {
		[lanhopBoard(name) releaseGlobally];
	}
}
