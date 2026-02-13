FROM ubuntu:22.04

# Avoid interactive prompts during package install.
ENV DEBIAN_FRONTEND=noninteractive

# Install base tools.
RUN apt-get update && apt-get install -y \
    curl \
    git \
    build-essential \
    ca-certificates \
    nodejs \
    npm \
    && rm -rf /var/lib/apt/lists/*

# Install Claude Code.
RUN npm install -g @anthropic-ai/claude-code

# Copy the pre-built h2 binary.
COPY h2 /usr/local/bin/h2
RUN chmod +x /usr/local/bin/h2

# Set up working directory.
WORKDIR /root

CMD ["/bin/bash"]
