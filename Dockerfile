FROM python:3.11-slim
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY ntp_sampler.py server.py dashboard.html .
EXPOSE 8080
CMD ["python3", "-u", "server.py"]
