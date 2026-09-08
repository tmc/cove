#import <Foundation/Foundation.h>
#import <objc/message.h>
#include <dlfcn.h>
int main(void) { @autoreleasepool {
 if(!dlopen("/System/Library/Frameworks/Virtualization.framework/Virtualization",RTLD_NOW))return 1;
 Class c=NSClassFromString(@"VZVirtualMachineStartOptions");
 for(unsigned ec=0;ec<64;ec++) {
  id options=[[c alloc] init];
  @try {
   ((void(*)(id,SEL,id))objc_msgSend)(options,sel_registerName("_setDelegatedExceptionClasses:"),@[@(ec)]);
   printf("EC 0x%02x setter accepted\n",ec);
  } @catch(NSException *e) {printf("EC 0x%02x rejected: %s\n",ec,[[e reason] UTF8String]);}
  [options release];
 }
} }
