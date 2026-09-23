#include "bridge.h"

#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include "quickjs.h"

typedef struct {
    int64_t deadline_ns;
} GatewayDeadline;

static int64_t gateway_now_ns(void) {
    struct timespec now;
    if (clock_gettime(CLOCK_MONOTONIC, &now) != 0) {
        return INT64_MAX;
    }
    return (int64_t)now.tv_sec * 1000000000 + now.tv_nsec;
}

static int gateway_interrupt(JSRuntime *runtime, void *opaque) {
    (void)runtime;
    GatewayDeadline *deadline = (GatewayDeadline *)opaque;
    return gateway_now_ns() >= deadline->deadline_ns;
}

static char *gateway_copy(const char *value, size_t length) {
    char *copy = malloc(length + 1);
    if (copy == NULL) {
        return NULL;
    }
    memcpy(copy, value, length);
    copy[length] = '\0';
    return copy;
}

#define GATEWAY_COPY_LITERAL(value) gateway_copy(value, sizeof(value) - 1)

static void gateway_capture_exception(JSContext *context, char **error_message) {
    JSValue exception = JS_GetException(context);
    const char *message = JS_ToCString(context, exception);
    if (message != NULL) {
        *error_message = gateway_copy(message, strlen(message));
        JS_FreeCString(context, message);
    }
    if (*error_message == NULL) {
        *error_message = GATEWAY_COPY_LITERAL("QuickJS execution failed");
    }
    JS_FreeValue(context, exception);
}

int gateway_qjs_run(const char *source, const char *input_json,
                    size_t memory_limit, long timeout_ms,
                    char **output_json, char **error_message) {
    *output_json = NULL;
    *error_message = NULL;
    if (source == NULL || input_json == NULL || memory_limit == 0 || timeout_ms <= 0) {
        *error_message = GATEWAY_COPY_LITERAL("invalid QuickJS input");
        return -1;
    }

    JSRuntime *runtime = JS_NewRuntime();
    if (runtime == NULL) {
        *error_message = GATEWAY_COPY_LITERAL("cannot create QuickJS runtime");
        return -1;
    }
    JS_SetMemoryLimit(runtime, memory_limit);
    JS_SetMaxStackSize(runtime, 512 * 1024);
    GatewayDeadline deadline = {
        .deadline_ns = gateway_now_ns() + (int64_t)timeout_ms * 1000000,
    };
    JS_SetInterruptHandler(runtime, gateway_interrupt, &deadline);

    // JS_NewContext adds JavaScript intrinsics only. No std/os modules,
    // loaders, network, or host functions are registered.
    JSContext *context = JS_NewContext(runtime);
    if (context == NULL) {
        *error_message = GATEWAY_COPY_LITERAL("cannot create QuickJS context");
        JS_FreeRuntime(runtime);
        return -1;
    }

    int result = -1;
    JSValue function = JS_UNDEFINED;
    JSValue input = JS_UNDEFINED;
    JSValue output = JS_UNDEFINED;

    function = JS_Eval(context, source, strlen(source), "gateway-automation.js", JS_EVAL_TYPE_GLOBAL);
    if (JS_IsException(function)) {
        gateway_capture_exception(context, error_message);
        goto cleanup;
    }
    if (!JS_IsFunction(context, function)) {
        *error_message = GATEWAY_COPY_LITERAL("automation source is not callable");
        goto cleanup;
    }

    input = JS_ParseJSON(context, input_json, strlen(input_json), "gateway-input.json");
    if (JS_IsException(input)) {
        gateway_capture_exception(context, error_message);
        goto cleanup;
    }

    output = JS_Call(context, function, JS_UNDEFINED, 1, &input);
    if (JS_IsException(output)) {
        gateway_capture_exception(context, error_message);
        goto cleanup;
    }

    size_t output_length = 0;
    const char *output_text = JS_ToCStringLen(context, &output_length, output);
    if (output_text == NULL) {
        gateway_capture_exception(context, error_message);
        goto cleanup;
    }
    *output_json = gateway_copy(output_text, output_length);
    JS_FreeCString(context, output_text);
    if (*output_json == NULL) {
        *error_message = GATEWAY_COPY_LITERAL("cannot copy QuickJS output");
        goto cleanup;
    }
    result = 0;

cleanup:
    JS_FreeValue(context, output);
    JS_FreeValue(context, input);
    JS_FreeValue(context, function);
    JS_FreeContext(context);
    JS_FreeRuntime(runtime);
    return result;
}
