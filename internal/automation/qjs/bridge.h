#ifndef GATEWAY_QJS_BRIDGE_H
#define GATEWAY_QJS_BRIDGE_H

#include <stddef.h>

int gateway_qjs_run(const char *source, const char *input_json,
                    size_t memory_limit, long timeout_ms,
                    char **output_json, char **error_message);

#endif
