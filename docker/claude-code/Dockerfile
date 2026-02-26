FROM golang:1.26.0-alpine

RUN apk add --no-cache \
    docker-cli docker-cli-compose \
    nodejs npm \
    bash git make curl \
    openssh-client jq redis

RUN npm install -g @anthropic-ai/claude-code

RUN mkdir -p /home/claude/.cache/go-build /home/claude/go/pkg/mod /workspace \
    && chmod -R 0777 /home/claude /workspace \
    && git config --system --add safe.directory /workspace

COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

WORKDIR /workspace
ENV HOME=/home/claude \
    GOPATH=/home/claude/go \
    GOMODCACHE=/home/claude/go/pkg/mod \
    GOCACHE=/home/claude/.cache/go-build

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["sleep", "infinity"]
