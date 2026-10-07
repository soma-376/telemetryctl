#ifndef PULSEMETRY_TRAY_WINDOW_H
#define PULSEMETRY_TRAY_WINDOW_H
#include <stdbool.h>

void *pulsemetryTrayPanel(void *owner);
bool pulsemetryTrayVisible(void *panel);
void pulsemetryShowTray(void *panel);
void pulsemetryHideTray(void *panel);

#endif
