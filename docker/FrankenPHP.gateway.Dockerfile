FROM dunglas/frankenphp:builder AS builder

COPY --from=caddy:builder /usr/bin/xcaddy /usr/bin/xcaddy
COPY . /src/gateway
WORKDIR /src/gateway

# FrankenPHP's builder image provides PHP headers and libraries. The custom
# Gateway Caddy module is compiled into the same binary as FrankenPHP.
RUN CGO_ENABLED=1 \
    XCADDY_SETCAP=1 \
    CGO_CFLAGS="$(php-config --includes)" \
    CGO_LDFLAGS="$(php-config --ldflags) $(php-config --libs)" \
    xcaddy build \
        --output /tmp/frankenphp \
        --with github.com/dunglas/frankenphp/caddy \
        --with github.com/webong/gateway/src/spinner/cmd/bridge/caddy \
        --replace github.com/webong/gateway=/src/gateway

FROM dunglas/frankenphp AS runtime

COPY --from=builder /tmp/frankenphp /usr/local/bin/frankenphp

# Fail the image build if either Gateway module was omitted from the binary.
RUN frankenphp list-modules | grep -F 'http.handlers.gateway' \
    && frankenphp list-modules | grep -F 'gateway.smtp'
