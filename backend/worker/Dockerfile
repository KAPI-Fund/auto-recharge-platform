FROM node:20-bookworm-slim

ENV DEBIAN_FRONTEND=noninteractive \
    PIP_BREAK_SYSTEM_PACKAGES=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PIP_NO_CACHE_DIR=1 \
    PYTHONUNBUFFERED=1 \
    HCAPTCHA_SOLVER_PYTHON=/usr/bin/python3 \
    HCAPTCHA_SOLVER_SCRIPT=/app/src/legacy/hcaptcha/solver.py \
    PLAYWRIGHT_BROWSERS_PATH=/root/.cache/ms-playwright

# Chromium is used by the Node worker and by the Python solver through CDP.
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl fonts-liberation fonts-noto-cjk fonts-noto-color-emoji \
    python3 python3-pip python-is-python3 \
    libatk-bridge2.0-0 libatk1.0-0 libcups2 libdbus-1-3 \
    libdrm2 libgbm1 libglib2.0-0 libgtk-3-0 libnspr4 libnss3 \
    libx11-6 libx11-xcb1 libxcb1 libxcomposite1 libxdamage1 \
    libxext6 libxfixes3 libxrandr2 libxshmfence1 libxss1 libxtst6 \
    libasound2 libpango-1.0-0 libpangocairo-1.0-0 libcairo2 \
    libgl1 xdg-utils procps \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY package.json package-lock.json* ./
RUN npm ci --omit=dev --ignore-scripts

# Install the original KC-PAY-GPT hCaptcha solver dependencies, including the
# CPU-only PyTorch stack and Python Playwright runtime.
COPY requirements-hcaptcha.txt ./
RUN python3 -m pip install -r requirements-hcaptcha.txt \
    && npx playwright install chromium \
    && python3 -m playwright install chromium

COPY src ./src

RUN mkdir -p /app/runtime/screenshots /app/runtime/hcaptcha /app/runtime/legacy

ENV NODE_ENV=production \
    PORT=8091 \
    HEADFUL=0 \
    CDP_PORT=9222 \
    CDP_URL=http://127.0.0.1:9222

EXPOSE 8091
CMD ["npm", "start"]
