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
		if ([[board types] containsObject:lanhopConcealedType]) {
			return LANHOP_PASTEBOARD_PRIVATE;
		}
		NSString *string = [board stringForType:NSPasteboardTypeString];
		if (string == nil) {
			return LANHOP_PASTEBOARD_NOT_TEXT;
		}
		NSUInteger bytes = [string lengthOfBytesUsingEncoding:NSUTF8StringEncoding];
		if ((int64_t)bytes > limit) {
			return LANHOP_PASTEBOARD_TOO_LARGE;
		}
		NSData *data = [string dataUsingEncoding:NSUTF8StringEncoding];
		*length = (int64_t)[data length];
		*text = malloc([data length] + 1);
		memcpy(*text, [data bytes], [data length]);
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

void lanhop_pasteboard_release(const char *name) {
	@autoreleasepool {
		[lanhopBoard(name) releaseGlobally];
	}
}
