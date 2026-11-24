ps -eo pid,etime,cmd --forest | grep -v '\[' | tail -40
ls /opt; systemctl is-active docker; docker ps -a 2>&1 | head; tail -5 /var/log/apt/term.log 2>/dev/null; ufw status | head -3
