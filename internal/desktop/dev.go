package desktop

import "log"

// devCommand handles `waylauncher ... dev <command> [arg]` sent by a second
// process (the single-instance forwarding). DEVELOPMENT ONLY: it exists so a
// smoke test with the fake transport can drive a running Waylauncher without
// clicking, and is enabled only by WAYLAUNCHER_FAKE_TRANSPORT=1.
//
//	dev launch <app>   open the application (as the deep link does)
//	dev stop <app>     stop all its instances (as a confirmed Dock Quit does)
//	dev focus <app>    what a Dock click does
//	dev confirm <cancel|stop|ask>  answer Dock Quit confirmations without a dialog
//	dev dock <on|off>  the "Show running Linux apps in the Dock" setting
//	dev quit           quit Waylauncher (stops every instance first)
func (d *desktop) devCommand(args []string) bool {
	for i, a := range args {
		if a != "dev" || i+1 >= len(args) {
			continue
		}
		cmd, arg := args[i+1], ""
		if i+2 < len(args) {
			arg = args[i+2]
		}
		log.Printf("dev: %s %s", cmd, arg)
		go func() {
			switch cmd {
			case "launch":
				if _, err := d.svc.Open(arg); err != nil {
					log.Println("dev launch:", err)
				}
			case "stop":
				if n, err := d.svc.StopApp(arg); err != nil {
					log.Println("dev stop:", err)
				} else {
					log.Printf("dev stop: %d instance(s)", n)
				}
			case "focus":
				if d.dock != nil {
					if err := d.dock.FocusApp(arg); err != nil {
						log.Println("dev focus:", err)
					}
				}
			case "confirm":
				if d.dock != nil {
					d.dock.mu.Lock()
					d.dock.devAnswer = map[string]string{"cancel": "cancel", "stop": "stop"}[arg]
					d.dock.mu.Unlock()
				}
			case "dock":
				st := d.svc.Settings()
				st.ShowRunningInDock = arg == "on"
				if err := d.svc.SaveSettings(st); err != nil {
					log.Println("dev dock:", err)
				}
			case "quit":
				d.quitting.Store(false)
				d.beforeQuitDev()
			}
		}()
		return true
	}
	return false
}
