# DOS Makefile
# This spawns a msys shell to compile the overlay, hence the "WIN" and "NIX" paths

# Locations of installed stuff
MSYSCMD_WIN=C:\\msys64\\msys2_shell.cmd
GO_NIX=/c/Program\ Files/Go/bin/go

zyxoverlay.exe: zyxoverlay.go
	$(MSYSCMD_WIN) -defterm -mingw64 -no-start -here -c "$(GO_NIX) build zyxoverlay.go"

release: zyxoverlay.exe
	del zyxoverlay.zip
	powershell Compress-Archive -Path zyxoverlay.exe,zyxoverlay.ini,browser_parasite.js,images zyxoverlay.zip