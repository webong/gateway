import assert from 'node:assert/strict';
import test from 'node:test';
import { forwardEmail } from './email-worker.mjs';

const env = {
  GATEWAY_RECIPIENT: 'support@mail.yollof.com',
  GATEWAY_INGRESS_URL: 'https://gateway.example.test/email/worker-ingress/95bd9dc6-bdce-45cd-a1b7-f2563776617f',
  GATEWAY_INGRESS_TOKEN: 'private-token',
};
function email(bytes = new TextEncoder().encode('From: sender@gmail.com\r\n\r\nHello\r\n')) {
  const rejected = [];
  return { from: 'sender@gmail.com', to: 'support@mail.yollof.com', rawSize: bytes.length,
    raw: new Response(bytes).body, rejected, setReject(reason) { rejected.push(reason); } };
}

test('forwards unchanged MIME once and accepts only a durable Gateway receipt', async () => {
  const message = email();
  await forwardEmail(message, env, async (url, request) => {
    assert.equal(url, env.GATEWAY_INGRESS_URL);
    assert.equal(request.headers.Authorization, `Bearer ${env.GATEWAY_INGRESS_TOKEN}`);
    assert.equal(request.headers['X-Gateway-Recipient'], message.to);
    assert.equal(request.headers['Content-Type'], 'message/rfc822');
    assert.deepEqual(new Uint8Array(request.body), new TextEncoder().encode('From: sender@gmail.com\r\n\r\nHello\r\n'));
    return { status: 202 };
  });
  assert.deepEqual(message.rejected, []);
});

test('rejects on unavailable Gateway and does not silently accept', async () => {
  const message = email();
  await forwardEmail(message, env, async () => ({ status: 503 }));
  assert.equal(message.rejected.length, 1);
});

test('rejects wrong recipient and oversized messages before forwarding', async () => {
  let calls = 0;
  const send = async () => { calls++; return { status: 202 }; };
  const wrong = email(); wrong.to = 'other@mail.yollof.com';
  await forwardEmail(wrong, env, send);
  const large = email(); large.rawSize = 10 * 1024 * 1024 + 1;
  await forwardEmail(large, env, send);
  assert.equal(calls, 0);
  assert.equal(wrong.rejected.length, 1);
  assert.equal(large.rejected.length, 1);
});
