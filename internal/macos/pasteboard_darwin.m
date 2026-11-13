#include "pasteboard_darwin.h"

#import <AppKit/AppKit.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>

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
		NSArray<NSPasteboardType> *types = [board types];
		if ([types containsObject:lanhopConcealedType]) {
			return LANHOP_PASTEBOARD_PRIVATE;
		}
		for (NSString *files in @[ NSPasteboardTypeFileURL, @"NSFilenamesPboardType", @"com.apple.pasteboard.promised-file-url" ]) {
			if ([types containsObject:files]) {
				return LANHOP_PASTEBOARD_NOT_TEXT;
			}
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

static BOOL lanhopAppendTerminated(NSMutableData *joined, NSString *string) {
	NSData *bytes = [string dataUsingEncoding:NSUTF8StringEncoding];
	if (bytes == nil || [bytes length] == 0 || memchr([bytes bytes], '\0', [bytes length]) != NULL) {
		return NO;
	}
	[joined appendData:bytes];
	[joined appendBytes:"" length:1];
	return YES;
}

static char *lanhopCopy(NSData *data, int64_t *length) {
	char *copy = malloc([data length] + 1);
	if (copy == NULL) {
		return NULL;
	}
	if ([data length] > 0) {
		memcpy(copy, [data bytes], [data length]);
	}
	copy[[data length]] = '\0';
	*length = (int64_t)[data length];
	return copy;
}

static BOOL lanhopHoldsEscape(NSURL *url, NSString *escape) {
	return [[url absoluteString] rangeOfString:escape options:NSCaseInsensitiveSearch].location != NSNotFound;
}

static BOOL lanhopIsLocalFileURL(NSURL *url) {
	if (![url isFileURL] || [url user] != nil || [url query] != nil || [url fragment] != nil) {
		return NO;
	}
	if (lanhopHoldsEscape(url, @"%00") || lanhopHoldsEscape(url, @"%2F")) {
		return NO;
	}
	NSString *host = [url host];
	return [host length] == 0 || [host caseInsensitiveCompare:@"localhost"] == NSOrderedSame;
}

int lanhop_pasteboard_read_files(const char *name, int64_t limit, char **paths, int64_t *length, int64_t *count) {
	@autoreleasepool {
		NSPasteboard *board = lanhopBoard(name);
		NSArray<NSPasteboardType> *types = [board types];
		if ([types containsObject:lanhopConcealedType]) {
			return LANHOP_PASTEBOARD_PRIVATE;
		}
		if (![types containsObject:NSPasteboardTypeFileURL]) {
			return LANHOP_PASTEBOARD_NOT_FILES;
		}
		NSArray<NSURL *> *urls = [board readObjectsForClasses:@[ [NSURL class] ] options:nil];
		*count = (int64_t)[urls count];
		if (*count == 0) {
			return LANHOP_PASTEBOARD_NOT_FILES;
		}
		if (*count > limit) {
			return LANHOP_PASTEBOARD_TOO_MANY;
		}
		NSMutableData *joined = [NSMutableData data];
		for (NSURL *url in urls) {
			if (!lanhopIsLocalFileURL(url)) {
				return LANHOP_PASTEBOARD_NOT_FILES;
			}
			NSString *path = [[url filePathURL] path];
			if (path == nil || !lanhopAppendTerminated(joined, path)) {
				return LANHOP_PASTEBOARD_NOT_FILES;
			}
		}
		*paths = lanhopCopy(joined, length);
		if (*paths == NULL) {
			return LANHOP_PASTEBOARD_NO_MEMORY;
		}
		return LANHOP_PASTEBOARD_FILES;
	}
}

static NSURL *lanhopURLOfPath(const char *path) {
	struct stat info;
	BOOL directory = stat(path, &info) == 0 && S_ISDIR(info.st_mode);
	return [NSURL fileURLWithFileSystemRepresentation:path isDirectory:directory relativeToURL:nil];
}

static NSURL *lanhopURLOfAddress(const char *address) {
	NSString *string = [NSString stringWithUTF8String:address];
	if (string == nil) {
		return nil;
	}
	return [NSURL URLWithString:string];
}

static NSArray<NSURL *> *lanhopURLs(const char *joined, int64_t length, NSURL *(*urlOf)(const char *)) {
	NSMutableArray<NSURL *> *urls = [NSMutableArray array];
	const char *entry = joined;
	const char *end = joined + length;
	while (entry < end) {
		size_t size = strnlen(entry, (size_t)(end - entry));
		if (size == 0 || size == (size_t)(end - entry)) {
			return nil;
		}
		NSURL *url = urlOf(entry);
		if (url == nil) {
			return nil;
		}
		[urls addObject:url];
		entry += size + 1;
	}
	if ([urls count] == 0) {
		return nil;
	}
	return urls;
}

static int64_t lanhopWriteURLs(const char *name, NSArray<NSURL *> *urls, int markPrivate) {
	if (urls == nil) {
		return LANHOP_PASTEBOARD_REFUSED;
	}
	NSPasteboard *board = lanhopBoard(name);
	[board clearContents];
	if (![board writeObjects:urls]) {
		return LANHOP_PASTEBOARD_REFUSED;
	}
	if (markPrivate && ![board setString:@"" forType:lanhopConcealedType]) {
		return LANHOP_PASTEBOARD_REFUSED;
	}
	return (int64_t)[board changeCount];
}

int64_t lanhop_pasteboard_write_files(const char *name, const char *paths, int64_t length, int markPrivate) {
	@autoreleasepool {
		return lanhopWriteURLs(name, lanhopURLs(paths, length, lanhopURLOfPath), markPrivate);
	}
}

int64_t lanhop_pasteboard_write_urls(const char *name, const char *addresses, int64_t length) {
	@autoreleasepool {
		return lanhopWriteURLs(name, lanhopURLs(addresses, length, lanhopURLOfAddress), 0);
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

int lanhop_pasteboard_types(const char *name, char **types, int64_t *length) {
	@autoreleasepool {
		NSMutableData *joined = [NSMutableData data];
		for (NSString *type in [lanhopBoard(name) types]) {
			if (!lanhopAppendTerminated(joined, type)) {
				return 0;
			}
		}
		*types = lanhopCopy(joined, length);
		return *types != NULL;
	}
}

int lanhop_pasteboard_file_urls(const char *name, char **urls, int64_t *length) {
	@autoreleasepool {
		NSMutableData *joined = [NSMutableData data];
		for (NSPasteboardItem *item in [lanhopBoard(name) pasteboardItems]) {
			if (!lanhopAppendTerminated(joined, [item stringForType:NSPasteboardTypeFileURL])) {
				return 0;
			}
		}
		*urls = lanhopCopy(joined, length);
		return *urls != NULL;
	}
}

char *lanhop_file_reference_url(const char *path) {
	@autoreleasepool {
		NSString *string = [NSString stringWithUTF8String:path];
		if (string == nil) {
			return NULL;
		}
		NSURL *reference = [[NSURL fileURLWithPath:string] fileReferenceURL];
		if (reference == nil) {
			return NULL;
		}
		return strdup([[reference absoluteString] UTF8String]);
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
