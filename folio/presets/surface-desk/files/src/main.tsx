import "./index.css"
import { mountPage } from "@hollis-labs/surface-page"
import { galleryViews } from "@hollis-labs/surface-page/gallery"
import start from "./modules/start"

// The views of this app's modules, by module id (the id in each module's Go manifest).
// core adds the Components window (the gallery) to the apps menu; it draws every installed registry item.
mountPage({ modules: { start }, core: galleryViews })
