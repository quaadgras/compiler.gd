import os, resource, subprocess, sys
env = dict(os.environ, NODE_OPTIONS="")
subprocess.run(sys.argv[1:], env=env)
print("peak rss %d MB" % (resource.getrusage(resource.RUSAGE_CHILDREN).ru_maxrss // 1024))
