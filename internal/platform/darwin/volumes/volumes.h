#ifndef CHOIX_VOLUMES_H
#define CHOIX_VOLUMES_H

// Status of a ChoixVolumesResult.
enum {
	CHOIX_VOLUMES_OK = 0,
	// The call failed (an NSError or a caught Objective-C exception);
	// message says what happened.
	CHOIX_VOLUMES_FAILED = 1,
};

// ChoixVolumesResult is what every entry point returns. uuid, name and
// message are malloc'd and owned by the caller, who frees them with free();
// each may be NULL. Only the fields an entry point documents are set.
typedef struct {
	int status;
	char *uuid;
	char *name;
	int read_only;
	int removable;
	// flag is the boolean answer of choix_volumes_mounted and
	// choix_volumes_in_trash.
	int flag;
	char *message;
} ChoixVolumesResult;

// choix_volumes_info sets uuid (NULL when the volume has none), name,
// read_only and removable for the volume that holds path.
ChoixVolumesResult choix_volumes_info(const char *path);

// choix_volumes_mounted sets flag when a mounted volume's UUID string equals
// uuid.
ChoixVolumesResult choix_volumes_mounted(const char *uuid);

// choix_volumes_in_trash sets flag when NSFileManager reports that a Trash
// directory contains path. A failed lookup is CHOIX_VOLUMES_FAILED.
ChoixVolumesResult choix_volumes_in_trash(const char *path);

// choix_volumes_raise_for_test raises an NSException named name inside the
// same guard as the entry points above.
ChoixVolumesResult choix_volumes_raise_for_test(const char *name);

#endif
