FROM golang:1.23

RUN apt-get update \
    && apt-get install -y --no-install-recommends xvfb \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src
COPY go.mod ./
COPY cmd cmd
COPY internal internal
COPY test test
RUN CGO_ENABLED=0 go build -trimpath -tags containertest -o /usr/local/bin/ \
        ./cmd/lanhop ./test/containers/xdndsource ./test/containers/xdndtarget ./test/containers/xprobe ./test/containers/pair \
    && install -m 0755 test/containers/machine /usr/local/bin/machine \
    && rm -rf /root/.cache/go-build

RUN useradd --create-home --uid 1000 desk \
    && mkdir -p -m 1777 /tmp/.X11-unix \
    && mkdir -p /desk/central /desk/l1 /desk/l2 \
    && chown -R desk:desk /desk

USER desk
WORKDIR /home/desk
ENV HOME=/home/desk DISPLAY=:0 XDG_SESSION_TYPE=x11
ENTRYPOINT ["machine"]
