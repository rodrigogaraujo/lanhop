#include "drag_darwin.h"

#import <AppKit/AppKit.h>
#include <string.h>

char *lanhop_drag_pasteboard_name(void) {
	@autoreleasepool {
		const char *name = [NSPasteboardNameDrag UTF8String];
		return name != NULL ? strdup(name) : NULL;
	}
}
