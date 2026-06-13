FROM python:3.11-slim

# non-root user
RUN useradd -m -u 1000 ntp

WORKDIR /app

# deps first (layer cache)
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# source
COPY ntp_sampler.py server.py dashboard.html .

# data dir — will be mounted as volume
RUN mkdir -p /var/lib/ntp && chown ntp:ntp /var/lib/ntp

USER ntp

EXPOSE 8080

ENV NTP_DB=/var/lib/ntp/ntp.db

HEALTHCHECK --interval=15s --timeout=5s --start-period=10s --retries=3 \
  CMD python3 -c "import urllib.request; urllib.request.urlopen('http://localhost:8080/ntp/status', timeout=4)"

CMD ["gunicorn", "server:app", \
     "--bind", "0.0.0.0:8080", \
     "--workers", "1", \
     "--worker-class", "gthread", \
     "--threads", "4", \
     "--graceful-timeout", "15", \
     "--timeout", "30", \
     "--access-logfile", "-", \
     "--error-logfile", "-"]
