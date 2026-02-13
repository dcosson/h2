FROM ubuntu:22.04

# Avoid interactive prompts during package install.
ENV DEBIAN_FRONTEND=noninteractive

# Install base tools.
RUN apt-get update && apt-get install -y \
    curl \
    git \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Install Node.js 20.x (Claude Code requires Node 18+).
RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y nodejs \
    && rm -rf /var/lib/apt/lists/*

# Install Claude Code.
RUN npm install -g @anthropic-ai/claude-code

# Copy the pre-built h2 binary (run "make build" before "h2 qa setup").
COPY h2 /usr/local/bin/h2
RUN chmod +x /usr/local/bin/h2

# Set up working directory.
WORKDIR /root

CMD ["/bin/bash"]
