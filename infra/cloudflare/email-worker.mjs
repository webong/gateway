// Cloudflare Email Routing Worker: forward unchanged RFC 5322 bytes to Gateway.
// Configure one exact recipient/route per Worker deployment.
const MAX_MIME_BYTES = 10 * 1024 * 1024;

export async function forwardEmail(message, env, send = fetch) {
  const recipient = String(message.to || '').toLowerCase();
  const configuredRecipient = String(env.GATEWAY_RECIPIENT || '').toLowerCase();
  const ingressURL = String(env.GATEWAY_INGRESS_URL || '');
  const token = String(env.GATEWAY_INGRESS_TOKEN || '');
  let target;
  try { target = new URL(ingressURL); } catch { target = null; }
  if (!configuredRecipient || recipient !== configuredRecipient || !target ||
      target.protocol !== 'https:' || !/^\/email\/worker-ingress\/[0-9a-f-]{36}$/.test(target.pathname) ||
      !token || message.rawSize > MAX_MIME_BYTES) {
    message.setReject('Gateway mail ingress is unavailable');
    return;
  }

  try {
    // Buffer only this bounded message: it avoids stream compatibility issues
    // and preserves the exact MIME bytes for Gateway's signature and callback.
    const raw = await new Response(message.raw).arrayBuffer();
    if (raw.byteLength === 0 || raw.byteLength > MAX_MIME_BYTES) {
      message.setReject('Gateway mail ingress is unavailable');
      return;
    }
    const response = await send(target.toString(), {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${token}`,
        'Content-Type': 'message/rfc822',
        'X-Gateway-Mail-From': String(message.from || ''),
        'X-Gateway-Recipient': String(message.to || ''),
      },
      body: raw,
      redirect: 'manual',
    });
    // Gateway returns 202 only after its durable queue accepts the message.
    if (response.status !== 202) message.setReject('Gateway mail ingress is unavailable');
  } catch {
    message.setReject('Gateway mail ingress is unavailable');
  }
}

export default {
  async email(message, env) { await forwardEmail(message, env); },
};
