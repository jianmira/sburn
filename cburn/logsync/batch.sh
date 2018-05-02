#/bin/sh
cat log |grep Processor > processor.log
cat log |grep Explorer  > explorer.log
cat log |grep Summary > summary.log
cat log |grep Finished > finish.log
cat log |grep Dispatch > dispatch.log
