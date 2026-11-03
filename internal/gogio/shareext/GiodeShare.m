// The share extension giode build embeds in iOS apps with
// app.share_target. It copies what's shared into the App Group's
// GiodeShare folder - a folder per share with the files and share.json -
// opens the app with its share URL scheme and finishes. The app (giode's
// Config.OnShareReceived) picks the share up when it becomes active.
#import <UIKit/UIKit.h>
#import <MobileCoreServices/MobileCoreServices.h>
#include <objc/message.h>

@interface GiodeShareViewController : UIViewController
@end

@implementation GiodeShareViewController {
	BOOL started;
	NSURL *dir;
	NSMutableArray *files;
	NSMutableArray *texts;
	NSString *subject;
	int count;
}

static NSString *infoString(NSString *key) {
	id v = [[NSBundle mainBundle] objectForInfoDictionaryKey:key];
	return [v isKindOfClass:[NSString class]] ? v : @"";
}

- (void)viewDidLoad {
	[super viewDidLoad];
	self.view.backgroundColor = [UIColor clearColor];
	UIActivityIndicatorView *spin = [[UIActivityIndicatorView alloc] initWithActivityIndicatorStyle:UIActivityIndicatorViewStyleLarge];
	spin.center = CGPointMake(CGRectGetMidX(self.view.bounds), CGRectGetMidY(self.view.bounds));
	spin.autoresizingMask = UIViewAutoresizingFlexibleTopMargin | UIViewAutoresizingFlexibleBottomMargin | UIViewAutoresizingFlexibleLeftMargin | UIViewAutoresizingFlexibleRightMargin;
	[spin startAnimating];
	[self.view addSubview:spin];
}

- (void)viewDidAppear:(BOOL)animated {
	[super viewDidAppear:animated];
	if (started) {
		return;
	}
	started = YES;
	[self collect];
}

- (void)finish {
	[self.extensionContext completeRequestReturningItems:@[] completionHandler:nil];
}

- (void)collect {
	NSFileManager *fm = [NSFileManager defaultManager];
	NSURL *container = [fm containerURLForSecurityApplicationGroupIdentifier:infoString(@"GiodeAppGroup")];
	if (container == nil) {
		NSLog(@"giode share: no App Group container for %@", infoString(@"GiodeAppGroup"));
		[self finish];
		return;
	}
	dir = [[container URLByAppendingPathComponent:@"GiodeShare" isDirectory:YES] URLByAppendingPathComponent:[NSUUID UUID].UUIDString isDirectory:YES];
	[fm createDirectoryAtURL:dir withIntermediateDirectories:YES attributes:nil error:nil];
	files = [NSMutableArray array];
	texts = [NSMutableArray array];
	subject = @"";
	dispatch_group_t group = dispatch_group_create();
	for (NSExtensionItem *item in self.extensionContext.inputItems) {
		if (subject.length == 0 && item.attributedTitle.length > 0) {
			subject = item.attributedTitle.string;
		}
		for (NSItemProvider *p in item.attachments) {
			dispatch_group_enter(group);
			[self load:p done:^{
				dispatch_group_leave(group);
			}];
		}
	}
	dispatch_group_notify(group, dispatch_get_main_queue(), ^{
		[self deliver];
	});
}

// fileType is the first type p offers that isn't text or a link: the
// type of a file, an image or a video.
static NSString *fileType(NSItemProvider *p) {
	for (NSString *t in p.registeredTypeIdentifiers) {
		CFStringRef ct = (__bridge CFStringRef)t;
		if (UTTypeConformsTo(ct, kUTTypeText) || UTTypeConformsTo(ct, kUTTypeURL)) {
			continue;
		}
		return t;
	}
	return nil;
}

- (void)load:(NSItemProvider *)p done:(void (^)(void))done {
	if ([p hasItemConformingToTypeIdentifier:(NSString *)kUTTypeFileURL]) {
		[p loadItemForTypeIdentifier:(NSString *)kUTTypeFileURL options:nil completionHandler:^(id item, NSError *err) {
			[self take:item provider:p type:nil];
			done();
		}];
		return;
	}
	NSString *t = fileType(p);
	if (t != nil) {
		[p loadFileRepresentationForTypeIdentifier:t completionHandler:^(NSURL *url, NSError *err) {
			// The file only lasts until this handler returns.
			[self take:url provider:p type:t];
			done();
		}];
		return;
	}
	NSString *kind = [p hasItemConformingToTypeIdentifier:(NSString *)kUTTypeURL] ? (NSString *)kUTTypeURL : (NSString *)kUTTypeText;
	if (![p hasItemConformingToTypeIdentifier:kind]) {
		done();
		return;
	}
	[p loadItemForTypeIdentifier:kind options:nil completionHandler:^(id item, NSError *err) {
		[self take:item provider:p type:nil];
		done();
	}];
}

// take records a loaded item: a file is copied into the share's folder,
// text and links are kept.
- (void)take:(id)item provider:(NSItemProvider *)p type:(NSString *)type {
	if ([item isKindOfClass:[NSURL class]] && ((NSURL *)item).isFileURL) {
		[self copyFile:item provider:p type:type];
	} else if ([item isKindOfClass:[NSURL class]]) {
		[self addText:((NSURL *)item).absoluteString];
	} else if ([item isKindOfClass:[NSString class]]) {
		[self addText:item];
	} else if ([item isKindOfClass:[NSAttributedString class]]) {
		[self addText:((NSAttributedString *)item).string];
	} else if ([item isKindOfClass:[NSData class]]) {
		NSString *s = [[NSString alloc] initWithData:item encoding:NSUTF8StringEncoding];
		if (s != nil) {
			[self addText:s];
		}
	} else if ([item isKindOfClass:[UIImage class]]) {
		NSData *png = UIImagePNGRepresentation(item);
		NSURL *tmp = [[NSURL fileURLWithPath:NSTemporaryDirectory()] URLByAppendingPathComponent:[[NSUUID UUID].UUIDString stringByAppendingPathExtension:@"png"]];
		if ([png writeToURL:tmp atomically:YES]) {
			[self copyFile:tmp provider:p type:(NSString *)kUTTypePNG];
			[[NSFileManager defaultManager] removeItemAtURL:tmp error:nil];
		}
	}
}

- (void)addText:(NSString *)s {
	if (s.length == 0) {
		return;
	}
	@synchronized(self) {
		[texts addObject:s];
	}
}

- (void)copyFile:(NSURL *)url provider:(NSItemProvider *)p type:(NSString *)type {
	BOOL scoped = [url startAccessingSecurityScopedResource];
	NSString *name = url.lastPathComponent;
	if (p.suggestedName.length > 0) {
		name = p.suggestedName;
		if (name.pathExtension.length == 0) {
			NSString *ext = url.pathExtension;
			if (ext.length == 0 && type != nil) {
				ext = (__bridge_transfer NSString *)UTTypeCopyPreferredTagWithClass((__bridge CFStringRef)type, kUTTagClassFilenameExtension);
			}
			if (ext.length > 0) {
				name = [name stringByAppendingPathExtension:ext];
			}
		}
	}
	name = [name stringByReplacingOccurrencesOfString:@"/" withString:@"_"];
	int n;
	@synchronized(self) {
		n = ++count;
	}
	NSString *file = [NSString stringWithFormat:@"%d", n];
	NSError *err = nil;
	if ([[NSFileManager defaultManager] copyItemAtURL:url toURL:[dir URLByAppendingPathComponent:file] error:&err]) {
		@synchronized(self) {
			[files addObject:@{@"file": file, @"name": name, @"order": @(n)}];
		}
	} else {
		NSLog(@"giode share: can't copy %@: %@", url, err);
	}
	if (scoped) {
		[url stopAccessingSecurityScopedResource];
	}
}

- (void)deliver {
	NSFileManager *fm = [NSFileManager defaultManager];
	if (files.count == 0 && texts.count == 0) {
		[fm removeItemAtURL:dir error:nil];
		[self finish];
		return;
	}
	NSArray *sorted = [files sortedArrayUsingComparator:^NSComparisonResult(NSDictionary *a, NSDictionary *b) {
		return [a[@"order"] compare:b[@"order"]];
	}];
	NSMutableArray *list = [NSMutableArray array];
	for (NSDictionary *f in sorted) {
		[list addObject:@{@"file": f[@"file"], @"name": f[@"name"]}];
	}
	NSDictionary *share = @{
		@"text": [texts componentsJoinedByString:@"\n"],
		@"subject": subject,
		@"files": list,
	};
	NSData *json = [NSJSONSerialization dataWithJSONObject:share options:0 error:nil];
	NSURL *tmp = [dir URLByAppendingPathComponent:@"share.json.tmp"];
	[json writeToURL:tmp atomically:YES];
	[fm moveItemAtURL:tmp toURL:[dir URLByAppendingPathComponent:@"share.json"] error:nil];
	[self openApp];
	[self finish];
}

// openApp opens the app through the responder chain's UIApplication:
// extensions can't call it directly.
- (void)openApp {
	NSURL *url = [NSURL URLWithString:[infoString(@"GiodeShareScheme") stringByAppendingString:@"://share"]];
	Class appClass = NSClassFromString(@"UIApplication");
	SEL sel = NSSelectorFromString(@"openURL:options:completionHandler:");
	for (UIResponder *r = self; r != nil; r = r.nextResponder) {
		if ([r isKindOfClass:appClass] && [r respondsToSelector:sel]) {
			((void (*)(id, SEL, NSURL *, NSDictionary *, id))objc_msgSend)(r, sel, url, @{}, nil);
			return;
		}
	}
}

@end
