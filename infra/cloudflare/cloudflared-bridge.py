"""Local TLS pass-through for a host cloudflared process into Docker Gateway.

Publish this container's port on host loopback only. Gateway's TLS certificate
is left intact so cloudflared can authenticate it with originServerName and
caPool in its existing ingress rule.
"""

import asyncio


async def copy(source: asyncio.StreamReader, destination: asyncio.StreamWriter) -> None:
    while chunk := await source.read(65536):
        destination.write(chunk)
        await destination.drain()


async def bridge(client_reader: asyncio.StreamReader, client_writer: asyncio.StreamWriter) -> None:
    try:
        gateway_reader, gateway_writer = await asyncio.open_connection("crm-gateway-1", 8443)
    except OSError:
        client_writer.close()
        await client_writer.wait_closed()
        return

    tasks = [
        asyncio.create_task(copy(client_reader, gateway_writer)),
        asyncio.create_task(copy(gateway_reader, client_writer)),
    ]
    try:
        await asyncio.wait(tasks, return_when=asyncio.FIRST_COMPLETED)
    finally:
        for task in tasks:
            task.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)
        gateway_writer.close()
        client_writer.close()
        await asyncio.gather(gateway_writer.wait_closed(), client_writer.wait_closed(), return_exceptions=True)


async def main() -> None:
    server = await asyncio.start_server(bridge, "0.0.0.0", 18443)
    async with server:
        await server.serve_forever()


if __name__ == "__main__":
    asyncio.run(main())
