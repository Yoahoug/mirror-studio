FROM node:20-slim AS frontend-builder

ARG NPM_REGISTRY=https://registry.npmmirror.com
ENV npm_config_registry=${NPM_REGISTRY}

WORKDIR /app

COPY package.json package-lock.json ./
COPY frontend/workspace/package.json frontend/workspace/package-lock.json ./frontend/workspace/
COPY frontend/console/package.json frontend/console/package-lock.json ./frontend/console/

RUN npm ci --fetch-retries=5 --fetch-retry-mintimeout=20000 --fetch-retry-maxtimeout=120000
RUN cd frontend/workspace && npm ci --fetch-retries=5 --fetch-retry-mintimeout=20000 --fetch-retry-maxtimeout=120000
RUN cd frontend/console && npm ci --fetch-retries=5 --fetch-retry-mintimeout=20000 --fetch-retry-maxtimeout=120000

COPY . .

RUN npm run build:css
RUN cd frontend/workspace && npm run build
RUN cd frontend/console && npm run build


FROM python:3.11-slim

ARG PIP_INDEX_URL=https://pypi.tuna.tsinghua.edu.cn/simple

ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1
ENV PIP_INDEX_URL=${PIP_INDEX_URL}
ENV PIP_DEFAULT_TIMEOUT=100
ENV PIP_RETRIES=10

WORKDIR /app

COPY requirements ./requirements
RUN pip install --no-cache-dir --retries 10 --default-timeout 100 -r requirements/prod.txt

COPY . .
COPY --from=frontend-builder /app/static /app/static

CMD ["gunicorn", "config.wsgi:application", "--bind", "0.0.0.0:8000"]
