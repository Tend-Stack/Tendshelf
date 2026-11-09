# Running Tendshelf on a server

Tendshelf is one program. These are examples for a Linux machine; adjust the names and paths. Every flag
below is in `tendshelf -h`.

## HTTPS is needed for offline and for installing the app

Browsers run a service worker only on `https://` (and on `localhost`). Without it there are no offline
downloads, no offline reading and no "Install app". Over plain `http://` on your network the player still
works. The easy route is Tailscale: with Tailscale running on the server and on your phone,

    tailscale serve --bg 8095

shares Tendshelf at `https://<machine>.<tailnet>.ts.net` for your tailnet only (`tailscale serve status`
shows the address; `tailscale serve reset` stops sharing). Start Tendshelf with `-trusted-proxy 127.0.0.1`
so it knows the request arrived over HTTPS. The other route is a reverse proxy with a certificate, below.

## systemd

Create a user once (`sudo useradd --system --create-home --home-dir /var/lib/tendshelf tendshelf`), copy the
binary to `/usr/local/bin/tendshelf`, and give that user read access to your audiobook folders. Save as
`/etc/systemd/system/tendshelf.service`:

    [Unit]
    Description=Tendshelf
    After=network.target

    [Service]
    User=tendshelf
    Environment=TENDSHELF_DATA=/var/lib/tendshelf
    ExecStart=/usr/local/bin/tendshelf -addr 127.0.0.1:8095 -trusted-proxy 127.0.0.1
    Restart=on-failure

    [Install]
    WantedBy=multi-user.target

Then `sudo systemctl enable --now tendshelf`, and `journalctl -u tendshelf` shows the first-run setup link.
Uploads (off by default; the owner turns them on per library) write into the folder the owner chose, so
that folder must be writable by this user; everything else is read-only to Tendshelf.
`-trusted-proxy` belongs only on a server that sits behind a proxy you run; with no proxy, leave it out.

## Caddy

Caddy gets the certificate by itself, sets the forwarding headers and has no body limit:

    tendshelf.example.com {
        reverse_proxy 127.0.0.1:8095
    }

## nginx

Uploads are sent in pieces of 8 MiB, and an ebook form can carry up to 200 MiB, so nginx's default
`client_max_body_size 1m` would refuse them. Streaming wants buffering off.

    server {
        listen 443 ssl;
        http2 on;
        server_name tendshelf.example.com;
        # ssl_certificate and ssl_certificate_key: your certificate (for example from certbot)

        client_max_body_size 256m;

        location / {
            proxy_pass http://127.0.0.1:8095;
            proxy_http_version 1.1;
            proxy_set_header Host $host;
            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
            proxy_set_header X-Forwarded-Proto $scheme;
            proxy_buffering off;
            proxy_request_buffering off;
            proxy_read_timeout 300s;
            proxy_send_timeout 300s;
        }
    }

With either proxy on the same machine, start Tendshelf with `-trusted-proxy 127.0.0.1`. For a proxy on
another machine give its address or a CIDR range (`-trusted-proxy 10.0.0.0/24`). Tendshelf believes
`X-Forwarded-For` and `X-Forwarded-Proto` only from those addresses.

## Docker

The repository has a `Dockerfile` and a `compose.yaml` (at the top, so `docker compose up` works from a
checkout). The image holds the server and Debian's `ffmpeg` and `ffprobe`; those are Debian's packages under
their own licences (mostly LGPL and GPL), installed from Debian's archive when you build. Nothing is published
to a registry: you build it yourself.

Build and run with compose:

    mkdir data
    docker compose up -d --build
    docker compose logs tendshelf

The logs show the first-run setup link. Edit `compose.yaml` first: the left side of the `/books` line is your
audiobook folder, and `UID` should be your own user id (`id -u`) so the container can write to `./data`.
Without compose:

    docker build --build-arg VERSION=2026.7.0 --build-arg UID=$(id -u) -t tendshelf .
    docker run -d --name tendshelf --restart unless-stopped -p 127.0.0.1:8095:8095 \
      -v "$PWD/data:/data" -v /srv/audiobooks:/books:ro tendshelf

- **Where things live.** `/data` is the data folder (`-data /data`): database, `ebooks`, `covers`,
  `dictionaries` and `backups`. Keep it in a volume or a host folder, never only inside the container. In the
  app, add `/books` as the library folder.
- **Listening.** The server listens on `0.0.0.0:8095` inside the container (`-addr`), because its default,
  `127.0.0.1`, cannot be reached from outside the container. The compose file publishes the port on
  `127.0.0.1` only. For HTTPS use a proxy (see Caddy and nginx below, or the commented Caddy service in
  `compose.yaml`) and add `-trusted-proxy <proxy address or range>` to the `command`.
- **Health.** The image checks `/healthz` itself (bash and grep; the server has no health command and the image
  has no curl). `docker ps` shows `healthy`.
- **File ownership.** The server runs as user `tendshelf`, uid 1000 unless you rebuild with `--build-arg UID=...`.
  The `./data` folder must be writable by that uid (`chown 1000 data`). The library only needs to be readable.
- **Read-only library.** `/books` is mounted `:ro`, which is what Tendshelf expects: it never writes to a library
  file. The one exception is uploads (off by default; the owner turns them on per library and chooses a folder).
  If you turn them on, mount that one subfolder writable on top of the read-only mount, for example
  `/srv/audiobooks/uploads:/books/uploads`, owned by the container's uid, and choose `/books/uploads` as the upload
  folder. The rest of the library stays read-only.
- **Backups.** The automatic database copies are in `data/backups`. To mirror the rest, mount another folder (for
  example `/backup`) and choose it on the Backups page. Stop the container before copying `data` by hand.
- **Upgrading.** Pull the new code, then `docker compose up -d --build`. The new version migrates the database
  when it starts, after writing a verified copy to `data/backups`. Going back to an older version needs one of
  those copies, as above.
- **Password reset.** `docker compose exec tendshelf tendshelf reset-password ...` (see the README).

## Backups and upgrades

Stop the server and copy the whole data folder (database, `ebooks`, `covers`, `dictionaries`, `backups`);
the automatic backups hold the database only unless a backup folder is set (Backups page), which also mirrors
the ebooks, covers, people, text and dictionaries. If that drive is unplugged, the next backup refuses and says so
instead of filling the system disk; choose the folder again on the Backups page to start a new backup there. Upgrading writes a copy of the database first; going back
to an older version needs one of those copies (see the README).
