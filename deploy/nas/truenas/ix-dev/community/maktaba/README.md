# Maktaba

[Maktaba](https://maktaba.tv/) is a home media server for films, series and recordings, in Arabic
and English, with live TV, subtitles and transcripts made on the home, and apps for phones and TVs.

After the first install, open the app's Shell (Workloads, maktaba, Shell) and run
**/opt/maktaba/server/bin/maktaba-server container setup**. Maktaba then starts by itself. The
full guide is at <a href="https://maktaba.tv/install/truenas">maktaba.tv/install/truenas</a>.

**Data:** a dataset on an SSD pool when there is one, with the **Apps** preset (or an ACL entry
for user 568 with Modify). Maktaba changes no permissions itself.

**Media:** give user 568 an ACL entry with Read (or Modify) on each media dataset. A folder
Maktaba cannot read shows No access with the fix: Datasets, the dataset, Permissions.

**Memory:** Maktaba sizes itself to the app's memory limit. ZFS's ARC gives memory back when it
is needed, and Maktaba counts it as available.

**GPU:** allocate an Intel or AMD GPU for video (VA-API), or an NVIDIA card for video and faster
speech to text. TrueNAS 25.10's NVIDIA drivers dropped Pascal and older cards: Maktaba then
converts on the CPU and says why.

**Updates:** Maktaba updates itself inside its update window, besides the catalog's updates.
