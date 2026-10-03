FROM caddy:2-alpine

# The upstream binary has cap_net_bind_service on the file. Copying it drops
# that file capability so a high-port listener can run with no-new-privileges
# and an empty capability set in Kubernetes.
RUN cp /usr/bin/caddy /usr/local/bin/gateway-caddy \
    && test -z "$(getcap /usr/local/bin/gateway-caddy)"

USER 10001:10001
ENTRYPOINT ["/usr/local/bin/gateway-caddy"]
CMD ["run", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"]
