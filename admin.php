<?php
session_start();
header('Content-Type: application/json');

require_once __DIR__ . '/lib.php';

$action = $_GET['action'] ?? '';

// Check authentication for administrative actions (except login/status/logout)
$needs_auth = !in_array($action, ['login', 'status', 'logout']);
$is_admin = !empty($_SESSION['admin_logged_in']);

if ($needs_auth && !$is_admin) {
    http_response_code(401);
    echo json_encode(['status' => 'error', 'message' => 'Unauthorized. Please log in first.']);
    exit;
}

try {
    switch ($action) {
        case 'status':
            echo json_encode([
                'status' => 'success',
                'logged_in' => $is_admin
            ]);
            break;

        case 'login':
            $password = $_POST['password'] ?? '';
            if (password_verify($password, ADMIN_PASSWORD_HASH)) {
                $_SESSION['admin_logged_in'] = true;
                echo json_encode([
                    'status' => 'success',
                    'message' => 'Logged in successfully.'
                ]);
            } else {
                http_response_code(401);
                echo json_encode([
                    'status' => 'error',
                    'message' => 'Invalid password.'
                ]);
            }
            break;

        case 'logout':
            $_SESSION['admin_logged_in'] = false;
            session_destroy();
            echo json_encode([
                'status' => 'success',
                'message' => 'Logged out successfully.'
            ]);
            break;

        case 'save_config':
            $config_json = $_POST['config_json'] ?? '';
            if (empty($config_json)) {
                throw new Exception("Configuration content cannot be empty.");
            }
            // Validate JSON
            $decoded = json_decode($config_json, true);
            if (json_last_error() !== JSON_ERROR_NONE) {
                throw new Exception("Invalid JSON format: " . json_last_error_msg());
            }

            // Write back to config.json
            $config_path = __DIR__ . '/config.json';
            $success = file_put_contents($config_path, json_encode($decoded, JSON_PRETTY_PRINT | JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES));
            if ($success === false) {
                throw new Exception("Failed to write to config.json.");
            }

            echo json_encode([
                'status' => 'success',
                'message' => 'Configuration saved successfully.'
            ]);
            break;

        default:
            throw new Exception("Invalid administrative action.");
    }

} catch (Exception $e) {
    http_response_code(500);
    echo json_encode([
        'status' => 'error',
        'message' => $e->getMessage()
    ]);
}
