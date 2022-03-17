#!/bin/sh

echo "Copying unit file"
cp /home/dmarcsyslogforwarder/dmarcsyslogforwarder.service /etc/systemd/system/dmarcsyslogforwarder.service
echo "reloading systemctl"
systemctl daemon-reload
echo "enabling service"
systemctl enable dmarcsyslogforwarder.service
systemctl start dmarcsyslogforwarder.service
# sleep some time to check if binary crashed
sleep 5
systemctl status dmarcsyslogforwarder.service
