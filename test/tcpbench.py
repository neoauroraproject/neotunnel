# Minimal TCP throughput meter for the e2e test (no iperf3 in the image).
#   server PORT                         serve on PORT
#   client HOST PORT down|up SECONDS    one connection; prints Mbit/s
# down: the server sends for SECONDS; up: the client sends and the server
# reports how many bytes it received.
import socket, struct, sys, threading, time

CHUNK = b"\0" * (256 << 10)


def serve(port):
    ls = socket.socket(socket.AF_INET6)
    ls.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    ls.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 0)
    ls.bind(("::", port))
    ls.listen(64)
    while True:
        c, _ = ls.accept()
        threading.Thread(target=handle, args=(c,), daemon=True).start()


def handle(c):
    try:
        mode = c.recv(1)
        if mode == b"D":
            secs = struct.unpack("!d", recvn(c, 8))[0]
            end = time.monotonic() + secs
            while time.monotonic() < end:
                c.sendall(CHUNK)
        elif mode == b"U":
            n = 0
            while True:
                b = c.recv(1 << 20)
                if not b:
                    break
                n += len(b)
            c.sendall(struct.pack("!Q", n))
    except OSError:
        pass
    finally:
        c.close()


def recvn(c, n):
    b = b""
    while len(b) < n:
        x = c.recv(n - len(b))
        if not x:
            raise OSError("closed")
        b += x
    return b


def client(host, port, mode, secs):
    c = socket.create_connection((host, port), timeout=secs + 30)
    start = time.monotonic()
    if mode == "down":
        c.sendall(b"D" + struct.pack("!d", secs))
        n = 0
        while True:
            b = c.recv(1 << 20)
            if not b:
                break
            n += len(b)
        took = time.monotonic() - start
    else:
        c.sendall(b"U")
        end = start + secs
        while time.monotonic() < end:
            c.sendall(CHUNK)
        c.shutdown(socket.SHUT_WR)
        n = struct.unpack("!Q", recvn(c, 8))[0]
        took = time.monotonic() - start
    print("%.1f" % (n * 8 / took / 1e6))


if __name__ == "__main__":
    if sys.argv[1] == "server":
        serve(int(sys.argv[2]))
    else:
        client(sys.argv[2], int(sys.argv[3]), sys.argv[4], float(sys.argv[5]))
